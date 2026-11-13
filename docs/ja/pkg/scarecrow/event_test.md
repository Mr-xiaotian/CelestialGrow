# pkg/scarecrow/event_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/scarecrow/event_test.go` は**イベントのフィールドが実行メトリクスを再構築するのに十分か**、およびイベントが配信過程で完全なペイロードと血縁 ID を保つかを検証します。`MetricsWatcher` 自体をテストするのではなく、独立したイベントプローブ（`metricsProbe`）でメトリクスの算出基準を再現し、「イベント定義」と「メトリクス実装」を分離して検証します。

## テストの重点

| テスト | 対象内容 |
|------|----------|
| `TestSyntheticLifecycleFeedsMetrics` | `Hub` 経由で全 11 種を網羅する 17 件の人工イベント列を順に注入し、Farm / Plot のライフサイクル、入力元の分類、結果カウント、上下流の流量、未完了数をアサートする |
| `TestEventsCarryPayloadAndIDs` | `SeedRipenEvent` / `SeedWitherEvent` / `SeedReplantEvent` / `SeedInputEvent` のペイロード、エラー、タイムスタンプ、`Attempt`、`Parents`、ID の相互異性が配信後も変わらないことをアサートする |

## テスト補助型

| 型 | 役割 |
|------|------|
| `nodeProbe` | 単一 Plot のプローブ状態：開始／終了フラグと時刻、`external`、`upstream`、`downstream`、`succeeded` / `failed` / `skipped` |
| `metricsProbe` | プローブの集合で、`Watcher` を実装する。`OnEvent` は `type switch` で `MetricsWatcher` と同じ基準に従って累積する |

`nodeProbe.pending()` は `external + Σupstream - (succeeded + failed + skipped)` を返し、`PlotMetrics.Pending` のアルゴリズムと同源です（ただしテストのイベント列は完全なため、0 の下限クランプはありません）。

## 主要なアサーションと設計意図

### 合成ライフサイクル：`TestSyntheticLifecycleFeedsMetrics`

イベント順序（`a`、`b` は 2 つの Plot で、`a → b` に辺が 1 本ある）：

```text
FarmStart
→ PlotStart(a) → PlotStart(b)
→ SeedInput(a, 外部 ×2) → SeedReplant(a) → SeedRipen(a) → SeedPrune(a)
→ SeedInput(b, From=a) → SeedRipen(b)
→ SeedInput(b, 外部 ×1) → SeedWither(b)
→ SealInput(a) → SealMerge(b)
→ PlotEnd(a) → PlotEnd(b) → FarmEnd
```

検証ポイント：

| アサーション | 説明 |
|------|------|
| `graphStarted && graphEnded` | Farm の先頭と末尾のイベントがどちらも観測され、配信でイベントが失われない |
| `a.started && a.ended`、`b` も同様 | Plot のライフサイクルイベントが完全 |
| `a.external == 2` | `From == ""` の 2 件の `SeedInputEvent` だけが外部入力として数えられる |
| `a` の結果カウント `(succeeded, failed, skipped) == (1, 0, 1)` | `SeedReplantEvent` はどの結果カウントにも入らない |
| `a.downstream["b"] == 1` | 上流からの配送は**送信側**で下流流量として記録される |
| `b.external == 1`、`b.upstream["a"] == 1` | 受信側は `From` で外部入力と上流入力を区別する |
| `b` の結果カウント `(1, 1, 0)` | 成熟と枯死がそれぞれ計上される |
| 両ノードの `pending() == 0` | 入力と結果が 1 対 1 で対応し、取りこぼしたイベントがない |

Hub は `WithErrorHandler(func(err error) { t.Errorf(...) })` を使用するため、オブザーバのエラーはどれもテストを直接失敗させます——これにより「人工イベント列が Hub のエラー経路を引き起こさない」ことも同時に検証されます。

### ペイロードと ID：`TestEventsCarryPayloadAndIDs`

- `WatcherFunc` でイベントを `[]Event` に収集し、その後 1 件ずつ**値型アサーション**（`got[0].(SeedRipenEvent)`）を行う——イベントが構造体値（ポインタではない）で配信されることを検証する。
- `success.Seed` と `seed`（匿名構造体値）を比較し、`success.Fruit == "fruit"` を確認する：`any` ペイロードがそのまま渡ることを検証する。
- `SeedID != FruitID`、`WeedID != SeedID`：生成側が下流イベントに新しい ID を割り当てなければならないことを検証する。
- `errors.Is(fail.Err, boom)`、`errors.Is(retry.Err, boom)`：`error` がラップされたり失われたりしていないことを検証する。
- `!success.Time.Equal(...)` は `==` ではなく `time.Equal` で比較し、`time.Time` 内部の単調クロックフィールドによる誤判定を避ける。
- `input.Parents == [2]` かつ `input.From == "a"`：配送された seed の血縁と送信元が同時に保たれることを検証する。

## 実行方法

```bash
go test ./pkg/scarecrow -run 'TestSyntheticLifecycleFeedsMetrics|TestEventsCarryPayloadAndIDs' -v
```

`-race` なしでも通ります。本ファイルは並行処理を扱わず、並行配信は [`hub_test.md`](./hub_test.md) がカバーします。

## 注意事項

- テストはイベント構造体を直接構築し、`pkg/plot` の生成ロジックを迂回します。したがって検証するのは**イベント契約がメトリクスを支えられるか**であり、生成側の値が妥当か（`Elapsed` の精度、`SeedRepr` の切り詰めなど）ではありません。
- 新しいイベント型を追加しても、`metricsProbe.OnEvent` の `type switch` はコンパイルエラーになりません（`Event` は閉じたインターフェースですが、`switch` は分岐を省略できる）。新しいイベントがプローブ状態に影響すべきかは人手で確認する必要があります。
- イベントのフィールド名を変更すると、本ファイルと `pkg/persist` のテストが同時に壊れます。これは想定された連鎖影響です。
