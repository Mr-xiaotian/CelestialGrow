# pkg/plot/plot_retry_test.go

> 📅 最終更新日: 2026/09/24

`plot_retry_test.go` は `Plot` の**再試行セマンティクス**に焦点を当てます。`WithMaxRetries` / `WithRetryDelay` / `WithRetryIf` の 3 つの Option が `basePlot.tend` の再試行ループにどう影響するかを検証し、最終的に `Plot.Harvest` を通じて結果をライフサイクルスナップショットとして確定させます。

## ケース

### `TestPlot_RetrySuccess` —— 再試行後に成功

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 2 {
          return 0, errors.New("transient error")
      }
      return seed * 10, nil
  }
  ```
- **設定**：`plot.NewPlot("test_retry_success", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3))`
- **seeds**：`[]int{1}`

検証：

1. `mustHarvest` が 1 件のレコードを返します。
2. `Status == "ripen"`、`FruitJSON == "10"` です。
3. `attempts.Load() == 3` です（最初の 2 回の失敗 + 3 回目の成功）。

> カバーのポイント：デフォルトの `retryDelay = 0` のとき、一時的なエラーが数回の再試行を経て成功でき、かつ `SeedRipen` ライフサイクルが正しく書き込まれること。

### `TestPlot_RetryExhausted` —— 再試行を使い果たしても失敗

- **cultivator**：`func(seed int) (int, error) { attempts.Add(1); return 0, errors.New("permanent error") }`（常に失敗）
- **設定**：`plot.NewPlot("test_retry_exhausted", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2))`
- **seeds**：`[]int{1}`

検証：

1. 1 件のレコード：`Status == "wither"`、`WitherMessage == "permanent error"` です。
2. `attempts.Load() == 3` です（元の 1 回 + 再試行 2 回）。

> カバーのポイント：`maxRetries+1` 回に達した後にループを抜けて `witherSeed` へ進み、最後の失敗では `SeedReplant` ログを**書かない**こと（このログは `attempt <= maxRetries` のときだけ書かれます）。

### `TestPlot_RetryIf` —— エラーフィルタが再試行を阻止する

- **cultivator**：常に `errors.New("permanent")` を返します。
- **設定**：`plot.NewPlot("test_retry_if", cultivator, plot.WithTenders(1), plot.WithMaxRetries(3), plot.WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }))`
- **seeds**：`[]int{1}`

検証：

1. 1 件のレコード：`Status == "wither"`、`WitherMessage == "permanent"` です。
2. `attempts.Load() == 1` です（初回で `retryIf` に阻止され、以降は再試行しません）。

> カバーのポイント：カスタムの `retryIf` が「再試行不可」のエラーを正確にフィルタできること。実務では `ErrPermanent` のようなエラーを再試行ループから除外するためによく使われます。

### `TestPlot_RetryDelay` —— カスタムの再試行間隔

- **cultivator**：
  ```go
  func(seed int) (int, error) {
      n := attempts.Add(1)
      if n <= 1 {
          return 0, errors.New("transient")
      }
      return seed, nil
  }
  ```
- **設定**：`plot.NewPlot("test_retry_delay", cultivator, plot.WithTenders(1), plot.WithMaxRetries(2), plot.WithRetryDelay(func(attempt int) time.Duration { return 100 * time.Millisecond }))`
- **seeds**：`[]int{1}`
- **追加**：`start := time.Now()`、`Run` の終了後に `elapsed := time.Since(start)`。

検証：

1. 1 件のレコード：`Status == "ripen"`、`FruitJSON == "1"` です。
2. `elapsed >= 100*time.Millisecond` であり、再試行の前に確かに 100ms 待ったことを証明します。

> カバーのポイント：`retryDelay(attempt)` は `attempt <= maxRetries` のときに `time.Sleep` から呼ばれ、指数バックオフや固定間隔のスロットリングの実装によく使われます。

## 共通の前提条件

- テストパッケージ：`package plot_test`（ブラックボックス）であり、`Plot` の未エクスポートフィールドには直接アクセスしません。
- `attempts` は `sync/atomic.Int32` を使いゴルーチンをまたいで安全に加算し、再試行回数のアサーションが並行の影響を受けないようにします。
- `plot_harvest_test.go` で定義された `mustHarvest(t, plot harvestable)`（`harvestable` はテスト内のローカルインターフェース `interface{ Harvest() ([]persist.LifecycleStatusRecord, error) }`）を再利用してスナップショットを取得するため、`Harvest` + `lifecycleSpout` の連携が暗黙的に検証されます。
- 4 つのケースはいずれも `WithTenders(1)` を使用します。並行な tender は総呼び出し回数を変えませんが、`attempts.Add(1)` のタイミングのアサーションを難しくするため、直列化してはじめて再試行回数が確定値になります。

## 実行方法

```bash
go test ./pkg/plot -run "TestPlot_Retry" -v
```

または plot パッケージのテストをまとめて実行します。

```bash
go test ./pkg/plot/... -v
```

## 注意事項

- 状態文字列はソースコードを基準とします。成功は `"ripen"`、失敗は `"wither"` です。結果フィールドは `FruitJSON`、エラーフィールドは `WitherMessage` です。
- `TestPlot_RetryDelay` は `elapsed >= 100*time.Millisecond` を下限として使い、厳密な等値は**行いません**。CI の揺らぎやスケジューリングの遅延で実際の所要時間が少し大きくなるのは想定どおりの挙動です。
- `TestPlot_RetryIf` は `errors.Is` のセマンティクスに依存します。将来 `retryIf` の内部が `==` 比較に変わった場合、本テストも同期して調整する必要があります。
- これらのケースは「`SeedReplant` ログが何回呼ばれたか」を明示的にアサートせず、`attempts` の合計と最終状態からのみ推測します。ログ回数をアサートする必要がある場合は `pkg/persist` のテストツールを併用する必要があります。
- `TestPlot_RetryExhausted` がアサートするのは `attempts.Load() == 3`、すなわち `1 + maxRetries` です。デフォルトの `maxRetries` を変更してもこのケースには影響しません（ケースが明示的に `WithMaxRetries(2)` を渡しているためです）。
