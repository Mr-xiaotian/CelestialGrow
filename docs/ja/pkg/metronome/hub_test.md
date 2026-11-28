# pkg/metronome/hub_test.go

> 📅 最終更新日: 2026/11/26

`pkg/metronome/hub_test.go` は `Hub` の 4 つの責務群をカバーします：**配信**（順序、周期フィルタ、ネスト）、**構造の検証**（自己参照、間接的な循環、重複登録）、**エラー隔離**（error、panic、ticker のハンドラ優先、既定ハンドラの panic）、**ライフサイクルの編成**（正順の起動、逆順の停止、失敗時のロールバック、実行期の追加・削除）。すべてのテストは `hub.OnTick(...)` を直接同期的に呼び、**`Clock` と実時間に依存しない**ため、完全に決定的で、スリープもタイムアウトもありません。

## 役割

- 「登録順 = 配信順 = 起動順、停止は逆順」という中核の契約をアサートとして固定します。
- 単一 ticker の失敗が同じラウンドの他の ticker に波及せず、エラーが `errors.Is` で元のエラーまで貫通できることを証明します。
- 実行期の `AddTicker` / `RemoveTicker` が、Hub が起動済みのときに直ちに `Lifecycle` と連動することを証明します。

## テスト一覧

| テスト関数 | テーマ | アサートの要点 |
|---------|------|---------|
| `TestHubFansOutInRegistrationOrder` | 配信順序 | 3 つの `TickerFunc` が順に `a, b, c` を記録 |
| `TestHubFiltersByTickerPeriod` | 周期フィルタ | 周期 2 は `1,3` を受信；周期 1 は `1,2,3,4` を受信 |
| `TestHubNestsChildHub` | 階層配信 | 親 Hub の 1 回の `OnTick` が子 Hub 内の ticker を 1 回発火させる |
| `TestHubRejectsCyclicRegistration` | サイクル検出 | 自己登録は失敗；`a→b` は成功；`b→a` は失敗 |
| `TestHubRejectsDuplicateRegistration` | 重複判定 | 同じポインタインスタンスの 2 回目の登録は失敗 |
| `TestHubIsolatesTickerError` | エラー隔離 | 健全な ticker も実行される；`errors.Is` 可能なエラーがちょうど 1 件報告される |
| `TestHubRecoversTickerPanic` | panic 隔離 | 健全な ticker も実行される；報告情報に `kaboom` を含む |
| `TestHubUsesTickerErrorHandlerBeforeDefaultReporter` | ハンドラの優先度 | ticker の `HandleTickError` が呼ばれ、Hub の既定ハンドラは呼ばれない |
| `TestHubSurvivesDefaultErrorHandlerPanic` | 受け皿 | 既定ハンドラの panic 後も配信を継続 |
| `TestHubLifecycleStartStopOrder` | ライフサイクルの順序 | `start:a, start:b, stop:b, stop:a` |
| `TestHubStartRollbackOnFailure` | 起動のロールバック | `start:a, start:b, stop:a`、`Start` はエラーを返す |
| `TestHubAddTickerStartsImmediatelyWhenRunning` | 実行期の追加 | 起動済み Hub 上の `AddTicker` が即座に `start:a` |
| `TestHubRemoveTickerStopsImmediatelyWhenRunning` | 実行期の削除 | `start:a, stop:a` |

## 各テストの詳細

### 配信とフィルタ

#### `TestHubFansOutInRegistrationOrder`

- `WithErrorHandler(func(error) {})` で既定ハンドラを黙らせます（本テストはエラーを発生させないため、防御的な設定）。
- 3 つの `TickerFunc`（`tag` は `a/b/c`）を登録し、それぞれがミューテックス保護の下で `got` に追加します。
- `hub.OnTick(TickEvent{Seq: 1})` の後に `[]string{"a", "b", "c"}` をアサートします。
- **暗黙の検証**：`TickerFunc` は比較不能で `sameTicker` は重複判定しないため、3 つの関数アダプタが共存できます。もし同じ `*struct` ポインタに変えたら、2 回目の登録は拒否されます。
- 共有スライス `got` のロックは「配信が同期である」ことを表現するためだけで、実際には並行はありません。

#### `TestHubFiltersByTickerPeriod`

- 2 つの `periodProbe`：`odd`（周期 2）、`all`（周期 1）で、登録順は `odd` が先。
- `OnTick` を 4 回連続で呼び、`Seq` はそれぞれ 1..4（**イベントは手で構築し、Clock を経由しない**）。
- `odd → [1,3]`、`all → [1,2,3,4]` をアサートします。すなわちフィルタは `(Seq-1)%period == 0` を使い、周期 2 は**奇数拍**に命中して偶数拍ではない。
- 境界としての価値：ここは最も間違えやすい勘所です（もし `Seq%period` を使うと、期待は `2,4` になる）。
- このテストは `Start` を呼んでおらず、**配信にライフサイクルの起動は不要**であることを示します。

#### `TestHubNestsChildHub`

- `child` 内に `calls` をインクリメントする `TickerFunc` を登録し、`parent.AddTicker(child)` する。
- `parent.OnTick(TickEvent{Seq: 1})` の後に `calls == 1`。
- `Hub` 自身が `Ticker` を満たし、親ノードの子項目として使えることを検証します。配信が同期で重複配送がないことも間接的に示します。

### 構造の検証

#### `TestHubRejectsCyclicRegistration`

```go
a.AddTicker(a) // 自环 → error
a.AddTicker(b) // 成功
b.AddTicker(a) // a 已在 b 的子树中 → error
```

- `err == nil / != nil` のみをアサートし、エラーテキストは検証しません。したがって「cannot add a hub to itself」と「cyclic hub registration rejected」の表現は独立に進化できます。
- 順序が鍵：まず `a→b` が成功した後でなければ、`b→a` は間接的な循環になりません。

#### `TestHubRejectsDuplicateRegistration`

- 同じ `*periodProbe` を 2 回登録し、2 回目は必ず失敗します。
- `sameTicker` が比較可能なポインタ型に対してインスタンスの等価判定を行うことに依存します。`TickerFunc` や slice を含む値型に変えると失敗しません（`ticker.md` 参照）。

### エラー隔離

#### `TestHubIsolatesTickerError`

- 登録順は意図的に「先に壊れたもの、後に健全なもの」：壊れたものは `boom` を返し、健全なものは `calls` をインクリメントします。
- アサート：`calls == 1`（壊れたものが配信を中断しない）、`len(reported) == 1`（ちょうど 1 回報告）、`errors.Is(reported[0], boom)`（`%w` のラップが貫通可能）。
- もし `deliver` がラップせず直接 `fmt.Errorf("%v")` に変わると `errors.Is` は失敗します——これはエラーチェーンへの強い制約です。

#### `TestHubRecoversTickerPanic`

- 1 つ目の ticker が `panic("kaboom")` し、2 つ目は健全です。
- 健全な ticker が依然として呼ばれ、報告されたエラーテキストに `kaboom` が含まれることをアサートします（`strings.Contains` で、完全な形式は比較しない）。
- 意味：panic は `deliver` の `recover` によってエラーに変換され、テストプロセスをクラッシュさせません。

#### `TestHubUsesTickerErrorHandlerBeforeDefaultReporter`

- `handlingProbe` は `OnTick`（エラーを返す）と `HandleTickError`（`handled` に記録）を実装します。
- Hub には `WithErrorHandler` が設定されています（`reported` に記録）。
- `len(handled) == 1` かつ `len(reported) == 0` をアサートします：ticker 自身のハンドラがエラーを**完全に飲み込み**、Hub の既定ハンドラへは行きません。

#### `TestHubSurvivesDefaultErrorHandlerPanic`

- `WithErrorHandler` に渡した関数が直接 `panic("handler boom")` します。`log` 出力は `io.Discard` へリダイレクトされ、`defer` で復元されます。
- 「必ず失敗する」ticker と「健全な」ticker を登録し、`OnTick` の後に健全な ticker の `calls == 1`。
- `report` の `recover` が有効であることを示します：既定ハンドラのクラッシュは今回の配信を中断せず、エラーはログへ退避します。
- テストはログ内容をアサートせず（すでに discard 済み）、制御フローが継続することだけをアサートします。

### ライフサイクル

#### `TestHubLifecycleStartStopOrder`

- 2 つの `lifecycleProbe`（`a`、`b`）が 1 つの `lifecycleRecorder` を共有します。
- `Hub.Start()` → `Hub.Stop()` で、呼び出し順は `start:a, start:b, stop:b, stop:a` を期待します。
- 「`Start` は正順、`Stop` は逆順」の 2 つの規則を同時に固定します。`lifecycleProbe.OnTick` は常に `nil` を返し、本テストは配信に関心がありません。

#### `TestHubStartRollbackOnFailure`

- `a` は正常に起動し、`b` の `startErr = errors.New("boom")`。
- `hub.Start()` はエラーを返さなければならず、呼び出し順は `start:a, start:b, stop:a` を期待します——すなわち `b` の失敗後に**直ちに逆順で**起動済みの `a` をロールバックします。
- エラーテキストも `started` フラグもアサートしません。したがって「失敗後も `started` は false のままで再試行可能」という点は直接カバーされていません。

#### `TestHubAddTickerStartsImmediatelyWhenRunning`

- まず `hub.Start()`（このとき ticker はなく、`started = true`）、次に `AddTicker(a)`。
- 期待する順序は `start:a`：実行期に追加された `Lifecycle` 項目が直ちに起動されます。
- `defer hub.Stop()` は `stop:a` を追加しますが、アサートは `Stop` の前に起きるため影響を受けません。

#### `TestHubRemoveTickerStopsImmediatelyWhenRunning`

- まず `AddTicker(a)`（Hub は未起動なので `Start` は呼ばれない）、次に `Start()`（`start:a`）、そして `RemoveTicker(a)`。
- 期待する `start:a, stop:a`：取り外し後に直ちに停止します。
- 前のテストと補完的で、「登録時は未起動／実行時に初めて停止」というタイミングの組み合わせをカバーします。

## テストスタブの型

| 型 | 要点 | 説明 |
|------|--------|------|
| `periodProbe` | `mu` が `seqs` を保護し、`snapshot()` がコピーを返す | `Periodic` を実装し、フィルタと重複判定のテストに使用。`mu` は「クロックの goroutine から呼ばれる可能性」に備えた防御で、現在の同期テストでは必須ではない |
| `handlingProbe` | 関数フィールドで `onTick` / `onHandle` を注入し、**ロックなし** | `ErrorHandler` を実装。同期呼び出しではロック不要 |
| `lifecycleRecorder` | `mu` + `calls`、`add` / `snapshot` | ライフサイクルの呼び出し順を記録・スナップショットし、スナップショットは別名を避けるためコピーを返す |
| `lifecycleProbe` | `name`、`recorder`、`startErr`、`stopErr` | `Ticker` と `Lifecycle` の両方を実装。`Start`/`Stop` はまず記録してから既定のエラーを返す |

`lifecycleProbe.stopErr` は現在のテストでは使われておらず、予約フィールドと見なせます（停止エラーの集約経路はまだカバーされていません）。

## 並行性とタイミングの仮定

- **すべて同期**：`Hub.OnTick` はテスト goroutine で直接呼ばれ、`Lifecycle` の `Start`/`Stop` も同期的に実行されます。したがって `var calls int` のような裸のカウンタは安全で、`periodProbe.mu` は「本番環境では並行になり得る」ことを表現するだけです。
- **実クロックなし**：`TickEvent{Seq: n}` はテストが手で構築し、`FiredAt` は零値です——`Hub` の配信ロジックは `FiredAt` を読まないため、アサートに影響しません。
- **`t.Parallel()` なし**：`log.SetOutput` のグローバル置き換え（`TestHubSurvivesDefaultErrorHandlerPanic`）が他のテストと衝突しません。
- **順序に敏感**：複数のアサートが登録順に依存するため、新しいテストを追加するときに既存の登録チェーンの途中へ ticker を挿入し、期待する順序を更新しないままにしないでください。

## 実行方法

```bash
# 全部 Hub 测试
go test ./pkg/metronome/ -run 'TestHub' -v

# 按主题过滤
go test ./pkg/metronome/ -run 'TestHub(FansOut|Filters|Nests)' -v
go test ./pkg/metronome/ -run 'TestHub(Rejects|Lifecycle|Start|AddTicker|RemoveTicker)' -v

# 单个用例
go test ./pkg/metronome/ -run 'TestHubStartRollbackOnFailure' -v

# 重复执行（这些测试是确定性的，重复仍应全绿）
go test ./pkg/metronome/ -run 'TestHub' -count=50

# 竞态检测
go test ./pkg/metronome/ -run 'TestHub' -race
```

## 注意事項

- **アサートのスタイル**：順序には `slices.Equal`、エラーチェーンには `errors.Is`（`%w` に依存）、panic のテキストには `strings.Contains` を使います。エラーのラップ方法を変えるとき、前の 2 つは強い制約で、3 つ目は元の panic 値が残ることだけを要求します。
- **カバーされていない公開 API**（ソースには存在するが本テストファイルでは直接検証していない）：
  - `Contains`（および `TickerFunc` に対して常に `false` を返す振る舞い）；
  - ジェネリックな `Find[T]`（`pkg/assembly` のテストが間接的にカバー）；
  - `TickPeriod` / `SetTickPeriod` / `WithTickPeriod` の値と検証；
  - `AddTicker(nil)`、不正な周期のエラー分岐；
  - `RemoveTicker` の未登録項目と `nil` のエラー分岐；
  - `Stop` 時に複数の子項目の停止が失敗した場合の `errors.Join` 集約、および `stopReversed` の逆順エラー収集；
  - `Start` のロールバック自体も失敗した場合の `errors.Join(startErr, stopErr)` 分岐；
  - 配信スナップショットと並行する `AddTicker`/`RemoveTicker` の交錯（「取り外した後でも途中の拍が届く可能性」）。
- **`TestHubFansOutInRegistrationOrder` の `tag := tag`** はループ変数捕捉の明示的なコピーです。Go 1.22+ では不要になりましたが、残しても意味は変わりません。
- **これらのテストを並行化しないでください**：複数のスタブ型が「呼び出しが同じ goroutine で起きる」という前提に依存しています（`handlingProbe` はロックなし、`calls` は裸の `int`）。`t.Parallel()` + 実クロックに変えると競合を持ち込みます。
