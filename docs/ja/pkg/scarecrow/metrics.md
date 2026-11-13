# pkg/scarecrow/metrics.go

> 📅 最終更新日: 2026/11/13

## 役割

`pkg/scarecrow/metrics.go` はイベント列から**各 Plot の実行メトリクスを再構築**します：状態、開始／終了時刻、入力元、成熟／枯死／剪定のカウント、そして未完了数です。`MetricsWatcher` は純粋なメモリ上のアキュムレータ（子ゴルーチンなし、I/O なし）で、`Hub` に登録すると `Plot` の `GetSeedNum` / `GetFruitNum` / `IsFinish` などの照会にデータを提供し、`pkg/persist` のログオブザーバがカウントを補完する際の根拠にもなります。

## 主要オブジェクト

### `PlotStatus`

```go
type PlotStatus int

const (
    PlotNotStarted PlotStatus = iota // 尚未收到 PlotStartEvent
    PlotRunning                      // 正在运行
    PlotStopped                      // 已结束
)
```

| 定数 | 値 | `String()` の出力 |
|------|---|-----------------|
| `PlotNotStarted` | 0 | `not_started` |
| `PlotRunning` | 1 | `running` |
| `PlotStopped` | 2 | `stopped` |
| その他の値 | — | `not_started`（`String()` の `default` 分岐） |

> `PlotStatus` は `plot.GetState()` の `0=idle / 1=running / 2=done` と数値が一致しますが、両者は独立した型であり、相互に代入してはいけません。

### `PlotMetrics`

```go
type PlotMetrics struct {
    Plot   string
    Status PlotStatus

    StartTime time.Time
    EndTime   time.Time

    ExternalInput int
    UpstreamInput int
    InputTotal    int

    Succeeded int
    Failed    int
    Skipped   int
    Processed int
    Pending   int

    UpstreamCounts   map[string]int
    DownstreamCounts map[string]int
}
```

| フィールド | 意味 | 計算方法 |
|------|------|----------|
| `Plot` | Plot 名 | スナップショットのキー名 |
| `Status` | 実行状態 | `PlotStartEvent` / `PlotEndEvent` で更新され、初期値は `PlotNotStarted` |
| `StartTime` / `EndTime` | 開始／終了時刻 | イベントの `StartTime` / `EndTime` をそのまま使う。発生していないものはゼロ値のまま |
| `ExternalInput` | 外部注入された seed 数 | `SeedInputEvent.From == ""` のときに +1 |
| `UpstreamInput` | 上流から配送された seed 数 | `UpstreamCounts` の全値の合計 |
| `InputTotal` | 入力総数 | `ExternalInput + UpstreamInput` |
| `Succeeded` | 成熟数 | `SeedRipenEvent` ごとに +1 |
| `Failed` | 枯死数 | `SeedWitherEvent` ごとに +1 |
| `Skipped` | 剪定数 | `SeedPruneEvent` ごとに +1 |
| `Processed` | 処理済み数 | `Succeeded + Failed + Skipped` |
| `Pending` | 入力済みだが結果が出ていない数 | `InputTotal - Processed`。**0 未満なら 0 にする** |
| `UpstreamCounts` | 送信元 Plot → 配送数 | 上流からの配送のたびに受信側で加算 |
| `DownstreamCounts` | 送信先 Plot → 産出数 | 上流からの配送のたびに**送信側**で加算 |

`PlotMetrics` は**値のスナップショット**です：`UpstreamCounts` / `DownstreamCounts` は `GetPlot` / `GetPlots` のたびに再割り当てしてコピーされるため、呼び出し側が変更しても `MetricsWatcher` の内部状態には影響せず、データ競合も発生しません。

### `MetricsWatcher`

```go
type MetricsWatcher struct {
    mu    sync.RWMutex
    plots map[string]*plotMetrics
}

func NewMetricsWatcher() *MetricsWatcher
```

| メンバー | 説明 |
|------|------|
| `mu` | 読み書きロックで、`plots` とすべての `plotMetrics` を保護する。`OnEvent` は書き込みロック、読み取りインターフェースは読み取りロックを保持する |
| `plots` | Plot 名 → 内部の可変ストレージセル。最初の関連イベントで必要に応じて生成される（冪等） |
| `plot(name)`（未エクスポート） | 必要に応じてセルを作る。`plots` が `nil` なら先に初期化するため、**ゼロ値の `MetricsWatcher` も使用可能** |
| `snapshot(name)`（未エクスポート） | 呼び出し側がロックを保持している前提で、マップをコピーし派生フィールドを計算する |

内部ストレージセル `plotMetrics` とスナップショットの違い：セルは非公開フィールド（`external` / `success` / `fail` / `skip` / `upstream` / `downstream` / `status` / `start` / `end`）を使い、公開するのはスナップショット構造体だけです。

### 読み取りインターフェース

| メソッド | シグネチャ | 動作 |
|------|------|------|
| `GetPlot` | `func (m *MetricsWatcher) GetPlot(name string) (PlotMetrics, bool)` | 未登録ならゼロ値と `false` を返す。登録済みならコピーしたスナップショットを返す |
| `GetPlots` | `func (m *MetricsWatcher) GetPlots() map[string]PlotMetrics` | 登録済みの全 Plot の新しいマップを返す。データがないときは空のマップ（nil ではない）を返す |

## 主要なイベントマッピング

| イベント | メトリクスへの影響 |
|------|--------------|
| `PlotStartEvent` | `status = PlotRunning`、`start = ev.StartTime` |
| `PlotEndEvent` | `status = PlotStopped`、`end = ev.EndTime` |
| `SeedInputEvent`（`From == ""`） | `external++` |
| `SeedInputEvent`（`From != ""`） | 受信側で `upstream[From]++`。同時に `plot(From).downstream[Plot]++` |
| `SeedRipenEvent` | `success++` |
| `SeedWitherEvent` | `fail++` |
| `SeedPruneEvent` | `skip++` |
| `FarmStartEvent` / `FarmEndEvent` / `SeedReplantEvent` / `SealInputEvent` / `SealMergeEvent` | 無視し、Plot のメトリクスに影響しない |

```mermaid
flowchart LR
    PS[PlotStartEvent] --> C[plotMetrics ストレージセル]
    PE[PlotEndEvent] --> C
    SI[SeedInputEvent] -->|From が空| EXT[external++]
    SI -->|From が非空| UP[受信側 upstream[From]++<br/>送信側 downstream[Plot]++]
    SR[SeedRipenEvent] --> SUC[success++]
    SW[SeedWitherEvent] --> FAIL[fail++]
    SP[SeedPruneEvent] --> SKIP[skip++]
    C --> SNAP[snapshot<br/>InputTotal/Processed/Pending]
    EXT --> SNAP
    UP --> SNAP
    SUC --> SNAP
    FAIL --> SNAP
    SKIP --> SNAP
```

## 重要な詳細

- **セルはイベントとともに作られる**：`Plot` 名を持つイベントはどれも対応するセルを作ります。`SeedInputEvent` はさらに `From` の送信元セルも作ります。そのため `GetPlots()` には「一度も起動せず、血縁カウントにだけ現れた」Plot が含まれることがあり、その `Status` は `PlotNotStarted`、`InputTotal` は 0 になります。
- **状態は派生計算に参加しない**：`Status == PlotStopped` であっても、後から到着する seed イベントは引き続き加算されます。`MetricsWatcher` は「終了後は破棄する」という判断をしません。
- **`Pending` には下限 0 がある**：イベント列が不完全な場合（結果だけをリプレイし入力がない、など）は `Processed > InputTotal` になりますが、差分は 0 にクランプされ、負の値にはなりません。
- **再試行はカウントしない**：`SeedReplantEvent` は無視されるため、`Succeeded + Failed + Skipped` は入力と 1 対 1 のままで、`Pending` が再試行回数で汚染されることはありません。
- **並行安全**：`OnEvent` は全体を通して書き込みロックを保持し、`GetPlot` / `GetPlots` は読み取りロックを保持します。複数 Plot の並行イベントは直列化され、読み取りと書き込みは同じ `RWMutex` を共有します。
- **スナップショットの分離**：`GetPlot` / `GetPlots` が返す新しいマップは 1 層の深いコピー（値コピー + マップ再構築）であり、呼び出し側は安全に長期保持し、他のゴルーチンから読み取れます。
- **メトリクスに `Elapsed` は含まれない**：所要時間は `PlotMetrics` に入りません。必要なら `EndTime.Sub(StartTime)` で自分で計算するか、`PlotEndEvent.Elapsed` を直接消費してください。

## 使用例

### イベントを直接投入する

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    metrics := scarecrow.NewMetricsWatcher()
    now := time.Now()

    _ = metrics.OnEvent(scarecrow.PlotStartEvent{Plot: "harvester", Tenders: 2, StartTime: now})
    _ = metrics.OnEvent(scarecrow.SeedInputEvent{Plot: "harvester", SeedID: 1, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedRipenEvent{Plot: "harvester", SeedID: 1, FruitID: 2, Time: now})
    _ = metrics.OnEvent(scarecrow.SeedWitherEvent{Plot: "harvester", SeedID: 3, WeedID: 4, Time: now})

    snapshot, ok := metrics.GetPlot("harvester")
    if !ok {
        panic("plot not registered")
    }
    fmt.Println(snapshot.Status)                       // running
    fmt.Println(snapshot.InputTotal, snapshot.Pending) // 2 0
    fmt.Println(snapshot.Succeeded, snapshot.Failed)   // 1 1
}
```

### Hub に登録してから検索する

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    hub := scarecrow.NewHub()
    _ = hub.AddWatcher(scarecrow.NewMetricsWatcher())

    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "a", SeedID: 1})
    _ = hub.OnEvent(scarecrow.SeedInputEvent{Plot: "b", From: "a", SeedID: 2, Parents: []int{1}})

    metrics, ok := scarecrow.Find[*scarecrow.MetricsWatcher](hub)
    if !ok {
        panic("metrics watcher not found")
    }
    plots := metrics.GetPlots()
    fmt.Println(plots["a"].DownstreamCounts["b"]) // 1
    fmt.Println(plots["b"].UpstreamCounts["a"])   // 1
    fmt.Println(plots["b"].UpstreamInput)         // 1
}
```

`hub` は Plot の Hub でも、Farm Hub をネストした階層でもかまいません——`Find` はまず直属を調べ、次にネストを調べます。`basePlot.currentMetrics` はまさにこの使い方です。

## 注意事項

- `MetricsWatcher` は**実際に受け取ったイベント**だけを反映します。ある Plot/Farm の Hub に登録していなければ、そのイベントは見えません。
- 同じ `MetricsWatcher` を同じ Hub に重複登録しないでください：イベントが 2 回処理され、カウントが倍になります。
- `Status` は Plot のライフサイクルイベントだけで駆動されます。イベントを手動でリプレイする際に `PlotStartEvent` を送らないと、`Status` は `PlotNotStarted` のままです。
- 読み取りインターフェースが返すのはスナップショットであり、リアルタイムのビューではありません。連続する 2 回の `GetPlot` の間の新しいイベントは、前回の結果には反映されません。
- `persist.LogWatcher` に `PlotEndEvent` 時点で正確な成熟／枯死／剪定数を出力させたい場合、`MetricsWatcher` はそれより**先に**登録する必要があります（`pkg/assembly` はこの順序で組み立てています）。
- プロセスをまたぐ分析やディスクへの永続化が必要な場合は `pkg/persist` のライフサイクル／ログオブザーバを使用してください。それらは本ファイルのカウントのセマンティクスを再利用しています。
