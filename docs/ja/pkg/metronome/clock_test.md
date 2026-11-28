# pkg/metronome/clock_test.go

> 📅 最終更新日: 2026/11/26

`pkg/metronome/clock_test.go` は実時間で `Clock` を駆動し（偽クロックは使わない）、7 種類の振る舞いをカバーします：構築時の検証、基本的な節拍系列、ルート ticker の周期フィルタ、実行期の周期変更の起床、起動／停止の冪等性、ルート ticker のエラーハンドラの優先度、そして**エラーハンドラ自身が panic した後もクロックが生存すること**。テストはタイミングの仮定に大きく依存しますが、いずれも数倍の余裕を取っているため、既定では安定して動作します。

## 役割

- `Clock` の対外契約を固定する：`Seq` は 1 から始まり、周期フィルタは `Clock` 層で有効になり、`SetInterval` は「次の拍」で有効にならなければならず「古い周期の満了」ではない。
- `Stop` の冪等性と「ハンドラの panic でクロックを停止させてはならない」という信頼性の約束を固定する。
- `deliver` の 2 つのエラー経路（`OnTick` が error を返す、`HandleTickError` が panic する）に回帰保護を提供する。

## テスト一覧

| テスト関数 | カバーする振る舞い | 主要なタイミング |
|---------|---------|---------|
| `TestNewClockRejectsInvalidArguments` | 構築引数の検証 | なし（純粋に同期） |
| `TestClockTicksRoot` | 基本の発火 + `Seq` が 1 から増加 | 周期 10ms、3 拍、上限 200ms |
| `TestClockRespectsRootTickPeriod` | ルート ticker の `TickPeriod` フィルタ | 周期 10ms、3 拍受信（実際は 5 拍）、上限 250ms |
| `TestClockSetIntervalTakesEffectOnNextTick` | 周期変更で即座に起床 | 200ms → 10ms、120ms で判定 |
| `TestClockIdempotentStartStop` | 繰り返しの `Start` / `Stop` が `nil` を返す | なし（10ms の周期が 1 つだけ） |
| `TestClockUsesRootErrorHandler` | ルートが `ErrorHandler` を実装する場合に優先して呼ぶ | 周期 10ms、150ms 以内でポーリング |
| `TestClockSurvivesRootErrorHandlerPanic` | ハンドラの panic 後もクロックが動作を続ける | 周期 10ms、40ms 観察 |

## 各テストの詳細

### `TestNewClockRejectsInvalidArguments`

- 2 回構築する：`NewClock(0, root)` と `NewClock(time.Millisecond, nil)`、いずれも `err != nil` のみをアサートします。
- **エラーテキストは検証しない**ため、エラーメッセージの表現を調整してもテストは壊れません。ただし「ゼロ／負の周期」と「nil root」は拒否されなければなりません。
- 境界のヒント：`interval = 0` と `nil` が構築期の失敗経路の唯一の 2 つで、正の周期 + 非 nil root は常に成功します。

### `TestClockTicksRoot`

```go
root := &channelTicker{ch: make(chan int, 4)}
clock, _ := NewClock(10*time.Millisecond, root)
```

- 起動後、`root.ch` から 3 つの `Seq` を収集し、`slices.Equal(got, []int{1, 2, 3})` をアサートします。
- **2 点を検証**：`Seq` の先頭値が 1 であること、通常時は拍を落とさず番号も重複しないこと。
- タイミングの仮定：3 拍の公称所要時間は 30ms、タイムアウト予算は 200ms（約 6 倍の余裕）。`channelTicker` の channel 容量は 4 > 3 で、`OnTick` 内の送信が永久にブロックしないため、逆にクロックを遅らせることもありません。
- 後始末では `defer` で `Stop()` を呼びエラーを確認し、正常経路で `Stop` が 5 秒以内に join できることを検証します。

### `TestClockRespectsRootTickPeriod`

- ルートは `periodChannelTicker{period: 2}`、すなわち `Periodic` を実装して 2 を返します。
- `[]int{1, 3, 5}` を受け取ることを期待します——3 点を証明します：
  1. `Clock` はルートの `OnTick` を呼ぶ**前に** `shouldTick` のフィルタを行っている；
  2. フィルタで除外された拍も**依然として `Seq` を消費する**（そうでなければ `1, 2, 3` になる）；
  3. フィルタはクロックの停止や補発を引き起こさない。
- タイミング：3 回の配送を得るには 5 拍 ≈ 50ms を経る必要があり、タイムアウトは 250ms です。

### `TestClockSetIntervalTakesEffectOnNextTick`

- 周期 200ms で構築し、`Start` の後にまず `time.Sleep(20ms)` を行い、`run` がすでに約 200ms の timer 上で `select` していることを保証します。
- 次に `SetInterval(10 * time.Millisecond)` を呼び、起点 `start` を記録して最初の拍を待ちます：
  - 120ms 以内に受信 → 通過（起床が有効で、到達点がおよそ `prevFire + 10ms` であることを示す）；
  - 150ms でも受信なし → `t.Fatal`；
  - 受信したが所要時間 > 120ms → これも失敗と判定（「ちょうど古い timer の 200ms の位置で発火した」という偽の通過を防ぐ）。
- **これが `wakeCh` の存在する唯一の直接的な証拠**：起床がなければ、200ms の timer によって最初の拍は ~180ms 後に落ちます。
- このテストは「120ms より遅くない」ことだけを検証し、正確なグリッドは検証しないため、スケジューリングのジッタに寛容です。

### `TestClockIdempotentStartStop`

- `Start` を 2 回、`Stop` を 2 回連続して呼び、4 回の呼び出しがすべて `nil` を返さなければなりません。
- `Start` の「実行中ならそのまま返す」と `Stop` の「未実行ならそのまま返す」という 2 つの早期リターン分岐をカバーします。
- goroutine 数はアサートしないため、「`Start` の繰り返しでループが余分に 1 つ起動したか」は `Seq` 関連のテストで間接的にしか保証されません。本テストの責務は API の冪等性のみです。

### `TestClockUsesRootErrorHandler`

- ルートは `clockHandlingProbe`：`OnTick` は常に `errors.New("boom")` を返し、`HandleTickError` はエラーを `handled` に追加します。
- 起動後、5ms 間隔で `len(handled) == 0` をポーリングし、タイムアウトは 150ms です。
- アサートの重点：**少なくとも 1 回**のエラーがルートの `ErrorHandler` に渡されること（ログ出力だけではない）。エラーテキストは検査せず、ちょうど 1 回であることも要求しません（10ms 周期なら 150ms 以内に複数回発生します）。
- 後始末で `Stop()` を呼びエラーを確認します。
- ⚠️ 読み取り側の `len(handled)` はロックされていません（`clockHandlingProbe.mu` は書き込み経路のみを保護します）。厳密にはデータ競合が存在します。既定（`-race` なし）の実行では影響を受けませんが、`-race` でまれに報告される場合は `Clock` ではなくテスト自身の問題です。

### `TestClockSurvivesRootErrorHandlerPanic`

- まず `log` 出力を `io.Discard` へリダイレクトして既定ハンドラの大量出力を避け、`defer` で復元します。
- ルートの `OnTick` はエラーを返し、`HandleTickError` は**直接 panic("handler boom")** します。
- 40ms（≈4 拍）実行した後に `clock.IsRunning()` が `true` であることをアサートし、その後 `Stop()` の成功を確認します。
- アサートの重点：
  - `IsRunning()` が `true` であることは状態フラグが `Stop` で変更されていないことしか示しません。**本当の判断基準はテストプロセスがまだ生きていること**です：panic が `deliver` の外へ逃げれば `go test` バイナリ全体がクラッシュし、アサートまで到達しません；
  - `Stop` が 5 秒以内に正常に join できること（クロックの goroutine がすでに死んでいれば `doneCh` はとっくに閉じられ、`Stop` も直ちに `nil` を返すため、これは補助的なシグナルにすぎません）。
- タイミングの仮定：40ms あれば複数回の節拍が発生し、必然的に panic 経路を通ります。

## テストスタブの型

| 型 | メソッドセット | 用途 |
|------|--------|------|
| `channelTicker` | `OnTick` が `event.Seq` を `ch` へ書き込む | 最も単純な節拍コレクタで、周期は 1 とみなす |
| `periodChannelTicker` | `TickPeriod() = period` + `OnTick` が `ch` へ書き込む | 周期フィルタの検証 |
| `clockHandlingProbe` | `OnTick` / `HandleTickError` を関数フィールドで注入し、`mu` でコールバックを直列化 | エラーハンドラの優先度と panic 経路の検証 |

3 つはいずれも**ポインタレシーバ**で、`&channelTicker{...}` により登録します。`channelTicker` の channel はバッファ付きでなければならず、そうでなければ `OnTick` がクロックの goroutine をブロックし、タイミングのアサートを壊します。

## 実行方法

```bash
# 全部 Clock 测试
go test ./pkg/metronome/ -run 'TestClock|TestNewClock' -v

# 单个用例（定位时序问题时最有用）
go test ./pkg/metronome/ -run 'TestClockSetIntervalTakesEffectOnNextTick' -v
go test ./pkg/metronome/ -run 'TestClockSurvivesRootErrorHandlerPanic' -v

# 重复运行以暴露偶发时序问题
go test ./pkg/metronome/ -run 'TestClock' -count=20

# 显式检查数据竞争（注意上文提到的测试自身读写不同步）
go test ./pkg/metronome/ -run 'TestClock' -race
```

## 注意事項

- **テストは実時間を使う**ため、極端に負荷の高い CI マシンで大量の並行タスクと同時に走らせるのには向きません。アサートの余裕は公称所要時間の約 5～6 倍ですが、それでもハードリアルタイムの保証ではありません。
- **`log.SetOutput` はグローバル状態**：`TestClockSurvivesRootErrorHandlerPanic` が一時的に置き換え、`defer` で復元します。現在パッケージ内に `t.Parallel()` はないため、他のテストと相互干渉しません。
- **カバーされていない経路**（ソースを読む際の既知の空白であり、欠陥の一覧ではありません）：
  - `Stop` の 5 秒タイムアウトのエラー分岐；
  - `SetInterval` に非正の周期を渡したときのエラー返却；
  - `DefaultInterval` 定数値そのもの；
  - 長時間の `OnTick` が引き起こす「拍をスキップし、追いかけない」の正確な節拍系列（`skipped` の計算は直接アサートされていない）；
  - `Clock` の停止後に再度 `Start` したときの `Seq` のゼロリセット。
- **`defer` 内での `t.Fatalf`**（`TestClockTicksRoot` など）は、`Stop` が失敗すると現在のテスト goroutine を終了させます。他のテストを隠すことはありませんが、そのケースの失敗情報は `Stop()` のエラーだけになります。
