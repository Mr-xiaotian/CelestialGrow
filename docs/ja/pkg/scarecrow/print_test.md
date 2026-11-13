# pkg/scarecrow/print_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/scarecrow/print_test.go` は `PrintWatcher` の**出力フォーマットの契約**と**無視する範囲**を固定します：進捗行のプレフィックス、フィールド順、累計値、そして Farm / Seal / Replant イベントが何も出力しないことです。テストは未エクスポートの `newPrintWatcher(out, name)` で出力を `bytes.Buffer` に接続し、標準出力を汚さないようにしています。

## テストの分布

| テスト | 対象内容 |
|------|----------|
| `TestPrintWatcherReportsProgress` | 8 件のイベントを順に送り、8 行の出力テキストを行ごとにアサートする |
| `TestPrintWatcherIgnoresFarmAndSealEvents` | 進捗と無関係な 5 件のイベントを送り、バッファの長さが 0 であることをアサートする |

## 主要なアサーションと設計意図

### 出力フォーマット：`TestPrintWatcherReportsProgress`

イベント列と期待する出力（プレフィックスは `demo`）：

| イベント | 期待する行 |
|------|--------|
| `PlotStartEvent{}` | `[demo] start total=0` |
| `SeedInputEvent{}` ×3 | `[demo] total=1(+1)`、`total=2(+1)`、`total=3(+1)` |
| `SeedRipenEvent{}` | `[demo] succeeded=1(+1), total=3` |
| `SeedWitherEvent{}` | `[demo] failed=1(+1), total=3` |
| `SeedPruneEvent{}` | `[demo] skipped=1(+1), total=3` |
| `PlotEndEvent{}` | `[demo] finish total=3, skipped=1, succeeded=1, failed=1` |

検証ポイント：

- `PlotStartEvent` がどの入力よりも前に起きるため、先頭行は `total=0` です——同時に `total` が `SeedInputEvent` だけで駆動されることを示します。
- 結果行の `total` は 3 のままです。結果イベントが `total` を変えないことを示します。
- 締めの行のフィールド順は `total, skipped, succeeded, failed` に固定され、ソースの `printf` の引数順と一致します。順序を変えると即座にテストに捕捉されます。
- アサーションは `strings.Contains` による行ごとの一致で、テキスト全体の完全一致は要求しません。したがって行間に余分な出力があるかは制約しません（現在の実装では 1 イベントにつきちょうど 1 行）。

### 無視する範囲：`TestPrintWatcherIgnoresFarmAndSealEvents`

`FarmStartEvent`、`FarmEndEvent`、`SeedReplantEvent`、`SealInputEvent`、`SealMergeEvent` を順に送り、`out.Len() == 0` をアサートします。これにより「ノードとタスクの進捗だけに注目する」振る舞いが固定されます：Farm のライフサイクル、再試行、終了シグナルはどれも出力されません。

## 実行方法

```bash
go test ./pkg/scarecrow -run 'TestPrintWatcher' -v
```

## 注意事項

- テストは「出力にその行が含まれるか」だけを検証し、複数回の実行間でカウントがリセットされるかは検証しません。`PrintWatcher` 自体にリセット用のインターフェースはありません（[`print.md`](./print.md) 参照）。
- テストは並行呼び出しをカバーしていません：`PrintWatcher.mu` による直列化には並行アサーションがないため、ロックなしの実装に変えても本ファイルは失敗しません。
- `out` への書き込み失敗の経路（`printf` は `Fprintf` のエラーを明示的に破棄する）も、`NewPrintWatcher` が `os.Stdout` に書く既定の構築もカバーされていません。
- 出力文言を 1 行でも変更すると、本ファイルの期待文字列も同期させる必要があります。これは意図的な結合であり、フォーマットのずれを防ぐためのものです。
