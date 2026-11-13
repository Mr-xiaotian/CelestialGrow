# pkg/assembly/assembly_test.go

> 📅 最終更新日: 2026/11/13

本ファイルは `pkg/assembly` のエンドツーエンド組み立てテストで、外部テストパッケージ `assembly_test` を採用しています（公開 API のみを通じてアクセスし、非公開フィールドには触れません）。2 つのテスト関数はいずれも `starters` テーブル駆動で、`StartPlotResources` と `StartFarmResources` をそれぞれ 1 回ずつ実行し、2 つのエントリの振る舞いが一致することを確認します。

テストの中核となる検証チェーンは次のとおりです：**Hub を作成 → リソースを組み立て → Hub にイベントを送信 → Stop → Hub からライフサイクルオブザーバーを取得 → データベースパスで状態スナップショットを読み戻す**。したがって Hub 登録、イベント配信、SQLite への書き込み、`Stop` の冪等性をカバーしています。

## テスト対象

| テスト | カバーする能力 | 重要な不変条件 |
|------|----------|-----------|
| `TestStartResourcesAssembleLifecycle` | 2 つのエントリの完全な組み立て + イベントの永続化 + 重複 Stop + 実行後の照会 | 組み立てに成功すると非 nil の `Resources` を返す；`SeedInputEvent` が Hub 経由で SQLite に書き込まれる；`Stop` を連続 2 回呼んでも panic しない；`scarecrow.Find` で `*persist.LifecycleWatcher` を取得できる；`DBPath()` が非空；スナップショットの状態が `seed`、`SeedJSON` が `"42"` |
| `TestStartResourcesRejectNilHub` | 2 つのエントリの nil 境界 | `hub == nil` のとき非 nil の error を返し、`Resources` は nil |

テストでは各ケースに独立したサブテスト名（`StartPlotResources` / `StartFarmResources`）を付けており、失敗時にどのエントリか直接特定できます。

## 主要なテスト詳細

### 共有テーブル駆動フィクスチャ

```go
type startFunc func(hub *scarecrow.Hub, logLevel string) (*assembly.Resources, error)

var starters = []struct {
	name  string
	start startFunc
}{
	{"StartPlotResources", assembly.StartPlotResources},
	{"StartFarmResources", assembly.StartFarmResources},
}
```

- `startFunc` は 2 つのエントリの共通シグネチャを明示し、エントリを値として渡します。将来 2 つのエントリのシグネチャが分岐すれば、ここで先にコンパイルエラーになります。
- `starters` はパッケージレベル変数で、2 つのテスト関数が共有します。新しい組み立てエントリを追加するときはここに 1 行追加するだけで、両方のテストが自動的にカバーします。
- 2 つのエントリは現在どちらも `startResources` へ転送されるため、この記述が実際に検証しているのは「2 つのエントリが分岐していないこと」です。

### `TestStartResourcesAssembleLifecycle`

各エントリに対して次を実行します。

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
	t.Errorf("unexpected watcher error: %v", err)
}))

resources, err := starter.start(hub, "DEBUG")
if err != nil {
	t.Fatalf("%s() = %v", starter.name, err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
	Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
	t.Fatalf("OnEvent() = %v", err)
}

resources.Stop()
resources.Stop() // 幂等
```

要点：

1. **Hub のエラーハンドラは直接テスト失敗として記録する**：`Hub.OnEvent` はオブザーバーの障害を `ErrorHandler` に渡すだけで常に nil を返すため、ハンドラを介して「オブザーバーがイベント処理時にエラーを出した」ことを捕捉する必要があります。そうしなければオブザーバーの障害は静かに飲み込まれます。
2. **ログレベルに `DEBUG` を渡す**：`SeedInputEvent` は `LogWatcher` で `DEBUG` レベルのログにマッピングされます。`DEBUG` を渡せばこのイベントが最低レベルでフィルタされません（本テストはログファイルの内容を検証しません）。
3. **`Stop` を連続 2 回呼ぶ**：`Resources.stopOnce` を検証します。下位の `funnel.Spout.Stop` はキューチャネルを `close` するため、単純に重複呼び出しすると `close of closed channel` で panic します。したがってこの行は実際には冪等実装を守るものです。
4. **先に Stop してから照会**：`Stop` は消費コルーチンがキューを処理し終えて SQLite を閉じるまで待ちます。その後 `LoadLifecycleStatusesFromPath` がパスでデータベースファイルを開き直して読み取るため、フラッシュ前のデータを読むことを避けられます。

照会とアサーション：

```go
lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
if !ok {
	t.Fatal("lifecycle watcher is not registered")
}
dbPath := lifecycle.DBPath()
if dbPath == "" {
	t.Fatal("lifecycle database path is empty")
}

records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
if err != nil {
	t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
}
if len(records) != 1 {
	t.Fatalf("records = %d, want 1", len(records))
}
if records[0].Status != "seed" || records[0].SeedJSON != "42" {
	t.Fatalf("record = %#v, want seed/42", records[0])
}
```

- `scarecrow.Find` は Hub およびそのネストした Hub の中で最初の `*persist.LifecycleWatcher` を探し、オブザーバーが確かに**渡された hub** に登録されたことを検証します（Plot レベルでは直接登録、Farm レベルでも同様に Farm Hub に登録）。
- `DBPath()` が非空であることは、`LifecycleWatcher.Start` が `BeforeStart` まで実行されて SQLite ファイルを作成したことを示します。`Find` の成功と合わせて、このオブザーバーが起動済みかつ登録済みであることを確認できます。
- `len(records) == 1` は、その plot に入力イベント 1 件に対応する状態スナップショットが 1 つだけあることを検証します（スナップショットは入力イベント ID を主キーに upsert）。同時に、イベントが実際に消費されて永続化されたことも示します。
- `Status == "seed"` となるのは `SeedInputEvent` のみを送信し、後続の ripen / wither / prune イベントがないためです。`SeedJSON == "42"` は `persist` による `Seed` フィールドの JSON シリアライズ（`int` 42 → `42`）に由来します。

### `TestStartResourcesRejectNilHub`

```go
resources, err := starter.start(nil, "INFO")
if err == nil {
	t.Fatalf("%s(nil) = nil, want error", starter.name)
}
if resources != nil {
	t.Fatalf("%s(nil) resources = %#v, want nil", starter.name, resources)
}
```

- 「エラーがある、リソースが nil」という 2 つの契約のみをアサートし、具体的なエラーメッセージには一致させないため、エラーメッセージの調整でテストが壊れることはありません。
- 実装を見ると、この経路はオブザーバーを作成する前に戻るため、ディレクトリやコルーチンの副作用は発生しません。テスト自体は「エラーあり + リソースが nil」という戻り値の契約のみをアサートします。

## 実行方法

```bash
# 运行本包全部测试
go test ./pkg/assembly/...

# 仅运行本文件
go test ./pkg/assembly -run 'TestStartResources'

# 查看两个入口的子测试
go test ./pkg/assembly -run 'TestStartResources' -v
```

## 注意事項

- **実行時の生成物が残る**：テストは作業ディレクトリを設定しないため、`LifecycleRecordHandler` / `LogRecordHandler` は相対パスで `lifecycles/<日期>/grow_lifecycle(<时间>).sqlite3` と `logs/grow_log(<日期>).log` を作成します。位置は `go test` 実行時の作業ディレクトリ（通常は `pkg/assembly/`）です。テストにはクリーンアップ処理がなく、これらのディレクトリは `.gitignore` で無視されています。
- **カバーされていない経路**：
  - ログオブザーバーの内容（実際にファイルへ書き込まれるか、レベルフィルタが正しいか）はアサートしていません。
  - 指標オブザーバーの集計結果（`GetPlot` / `GetPlots`）はアサートしていません。
  - 起動失敗時の各種ロールバック分岐（`lifecycle.Start` / `log.Start` / `AddWatcher` の失敗）はカバーしておらず、失敗する handler や書き込み不可のディレクトリを注入しないと発生させられません。
  - `Resources` 内部の 2 つのオブザーバーの登録順序やオブザーバー数は明示的にアサートしておらず、`Find` はライフサイクルオブザーバーの存在しか証明できません。
- **`Stop` のエラーは破棄される**：テストは `resources.Stop()` を呼び出して戻り値を受け取りません。そもそもこのメソッドに戻り値がないためです。下位のフラッシュタイムアウトもテストを失敗させません。
- **エラーハンドラはオブザーバーの障害を即座に失敗させる**：`WithErrorHandler` 内の `t.Errorf` は現在のサブテストにのみ影響します。オブザーバーが panic した場合、Hub はそれをエラーに包んでこのハンドラに渡すため、同様に捕捉されます。
- **テストは実際のファイルシステムと SQLite ドライバに依存する**：`modernc.org/sqlite` が利用可能である必要があります。作業ディレクトリが書き込み不可またはディスクが満杯の場合、`Start` はエラーを返し、最初のテストが失敗します。
