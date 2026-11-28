# pkg/reporter/snapshot.go

> 📅 最終更新日: 2026/11/26

`pkg/reporter/snapshot.go` は**グラフレベルの状態スナップショット**の対外契約と周期ドライバを定義します：`MetricsView`（データ源）、`NodeStatus`（Web 側ペイロード）、`SnapshotSink`（書き込み先）、`SnapshotTicker`（拍ごとの投影）です。「指標 → ペイロード」の純粋な投影と転送だけを行い、**ネットワーク IO は一切実行しません**。

## 役割

- `scarecrow.PlotMetrics` を celestialflow-web と整合する `NodeStatus` に投影します。
- `SnapshotSink` を介してスナップショットを下流へ渡します。本番の組み立てでは下流は `PushWatcher` であり、そこから非同期に `/api/push_snapshot` へ POST されます。
- `metronome.Ticker`（`OnTick` + `TickPeriod` のみ）として拍 Hub に接続でき、`Start` / `Stop` は不要です。

## 主要オブジェクト

### 定数

```go
const DefaultSnapshotPeriod = 1 // 每拍都推
```

### `type MetricsView`（インターフェース）

```go
type MetricsView interface {
    GetPlots() map[string]scarecrow.PlotMetrics
}
```

| 取り決め | 説明 |
|------|------|
| キー | Plot 名 |
| 値 | `scarecrow.PlotMetrics` のスナップショット（状態、カウント、開始／終了時刻、上流／下流カウント） |
| 所有権 | 呼び出し側は読み取り専用で、返された map やその中のカウント辞書を変更してはいけない |
| スレッド安全性 | `GetPlots` は拍の goroutine から呼ばれる可能性があるため、実装側で自身がロックする必要がある |

標準実装は `*scarecrow.MetricsWatcher` です：読み取りロック内で指標とカウント辞書をコピーし、不変のスナップショットを返すため、そのまま `SnapshotTicker` に渡せます。

### `type NodeStatus`

```go
type NodeStatus struct {
    Status           int            `json:"status"`
    TasksInput       int            `json:"tasks_input"`
    TasksProcessed   int            `json:"tasks_processed"`
    TasksPending     int            `json:"tasks_pending"`
    TasksSucceeded   int            `json:"tasks_succeeded"`
    TasksFailed      int            `json:"tasks_failed"`
    TasksSkipped     int            `json:"tasks_skipped"`
    UpstreamCounts   map[string]int `json:"upstream_counts"`
    DownstreamCounts map[string]int `json:"downstream_counts"`
    StartTime        float64        `json:"start_time"`
    EndTime          float64        `json:"end_time"`
}
```

フィールド名と意味は celestialflow-web の `NodeStatus` と一致していなければなりません：

| Go フィールド | JSON キー | 型 | 意味 | 由来 |
|---------|---------|------|------|------|
| `Status` | `status` | `int` | `0` 未実行 / `1` 実行中 / `2` 停止済み | `int(PlotMetrics.Status)` |
| `TasksInput` | `tasks_input` | `int` | 入力タスクの総数（外部注入 + 上流からの投入） | `InputTotal` |
| `TasksProcessed` | `tasks_processed` | `int` | 処理済みの総数（成功 + 失敗 + スキップ） | `Processed` |
| `TasksPending` | `tasks_pending` | `int` | 入力済みだがまだ結果がない | `Pending` |
| `TasksSucceeded` | `tasks_succeeded` | `int` | 成功数 | `Succeeded` |
| `TasksFailed` | `tasks_failed` | `int` | 失敗数 | `Failed` |
| `TasksSkipped` | `tasks_skipped` | `int` | スキップ数 | `Skipped` |
| `UpstreamCounts` | `upstream_counts` | `map[string]int` | 各上流 → 本ノードへの投入数 | `UpstreamCounts` |
| `DownstreamCounts` | `downstream_counts` | `map[string]int` | 本ノード → 各下流への産出数 | `DownstreamCounts` |
| `StartTime` | `start_time` | `float64` | 開始の Unix 浮動小数点秒。未開始は `0` | `StartTime`（零値→`0`） |
| `EndTime` | `end_time` | `float64` | 終了の Unix 浮動小数点秒。未終了は `0` | `EndTime`（零値→`0`） |

> **`elapsed_time` を意図的に含みません**：この値は毎拍変化するため、サーバーが内容の等価なスナップショットを更新と誤判定してしまいます。フロントエンドは `start_time` / `end_time` とブラウザの時計から自身で導出します。

### `type SnapshotSink`（インターフェース）

```go
type SnapshotSink interface {
    PushSnapshot(snapshot map[string]NodeStatus) error
}
```

- 本番実装は `*PushWatcher` です（エンキュー後に非同期でプッシュ）。
- インターフェースには書き込みの意味しかなく、`Close` はありません。ライフサイクルは sink 自身が管理します。
- 返した error は `SnapshotTicker.OnTick` が**そのまま**上位へ返し、ラップしません。

### `type SnapshotTicker`

```go
type SnapshotTicker struct {
    metricsView MetricsView
    sink        SnapshotSink
    period      int
}
```

#### 構築：`NewSnapshotTicker`

```go
func NewSnapshotTicker(metricsView MetricsView, sink SnapshotSink, period int) (*SnapshotTicker, error)
```

検証順序とエラー：

| 順序 | 条件 | エラー |
|:----:|------|------|
| 1 | `metricsView == nil` | `reporter: metrics view is nil` |
| 2 | `sink == nil` | `reporter: snapshot sink is nil` |
| 3 | `period == 0` → `1` に置換；その後 `period < 1` | `reporter: snapshot period must be positive, got %d` |

- インターフェースの nil 判定は「インターフェース値が nil か」だけを見ます：`(*SomeView)(nil)` のような**型付き nil ポインタ**を渡しても拒否されず、`GetPlots` 呼び出し時に panic する可能性があります。
- 負の周期は既定値に退化せず、そのままエラーになります。

#### メソッド

| メソッド | シグネチャ | 動作 |
|------|------|------|
| `TickPeriod` | `func (t *SnapshotTicker) TickPeriod() int` | 有効になった周期を返す（既定値は埋め込み済み） |
| `OnTick` | `func (t *SnapshotTicker) OnTick(event metronome.TickEvent) error` | 指標を 1 回読み、投影して sink に渡す |

`OnTick` の詳細：

1. `event` はまったく使用されません。
2. `toStatusSnapshot(t.metricsView.GetPlots())` は毎回全量投影します：ノード数 N、カウント項目数 E の場合のコストは `O(N + E)`。
3. `return t.sink.PushSnapshot(snapshot)` を直接行い、エラーはラップもリトライもされません。
4. キャッシュも重複判定もしません：指標がまったく変化しなくても、毎拍等価なスナップショットを生成して転送します。重複判定はサーバーの責任です。

## 主要フロー

```mermaid
flowchart LR
    MW[MetricsWatcher<br/>scarecrow] -->|GetPlots| MV[MetricsView]
    MV -->|map[string]PlotMetrics| T[SnapshotTicker.OnTick]
    T -->|toStatusSnapshot| NS[map[string]NodeStatus]
    NS -->|SnapshotSink.PushSnapshot| PW[PushWatcher]
    PW -->|Inlet.Send| Q[(非同期キュー)]
    Q -->|POST /api/push_snapshot| API[報告サービス]
```

`SnapshotTicker` と `PushWatcher` の責務分担：

- ticker は**いつ**データを取るか（拍周期）を決める；
- watcher は**どう**送るか（セッション識別子、タイムスタンプ、JSON、リトライ方針=なし）を決める。

## 使用例

### スナップショットを直接消費する（カスタム sink）

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// printSink 把快照打印到标准输出，演示 SnapshotSink 的最小实现。
type printSink struct{}

func (printSink) PushSnapshot(snapshot map[string]reporter.NodeStatus) error {
    for name, status := range snapshot {
        fmt.Printf("%s: status=%d input=%d pending=%d\n",
            name, status.Status, status.TasksInput, status.TasksPending)
    }
    return nil
}

func main() {
    metrics := scarecrow.NewMetricsWatcher() // 实现 MetricsView

    ticker, err := reporter.NewSnapshotTicker(metrics, printSink{}, 0) // 0 → 每拍
    if err != nil {
        panic(err)
    }
    fmt.Println("period:", ticker.TickPeriod())

    // 通常由 metronome 驱动；这里手动触发一次。
    if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err != nil {
        fmt.Println("snapshot failed:", err)
    }
}
```

### 拍 Hub への登録（本番組み立ての等価な書き方）

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    // 指标源同时作为 MetricsView 传给快照 ticker 与推送观察者。
    metrics := scarecrow.NewMetricsWatcher()

    pushWatcher, err := reporter.NewPushWatcher("demo@1", "http://127.0.0.1:5005", metrics)
    if err != nil {
        panic(err)
    }
    ticker, err := reporter.NewSnapshotTicker(metrics, pushWatcher, 2) // 每 2 拍
    if err != nil {
        panic(err)
    }

    watcherHub := scarecrow.NewHub()
    if err := watcherHub.AddWatcher(pushWatcher); err != nil {
        panic(err)
    }
    tickerHub, err := metronome.NewHub()
    if err != nil {
        panic(err)
    }
    if err := tickerHub.AddTicker(ticker); err != nil { // SnapshotTicker 不是 Lifecycle
        panic(err)
    }
    clock, err := metronome.NewClock(time.Second, tickerHub)
    if err != nil {
        panic(err)
    }

    // 先启动 watcher（PushWatcher.Start），再启动节拍。
    if err := watcherHub.Start(); err != nil {
        panic(err)
    }
    if err := tickerHub.Start(); err != nil {
        panic(err)
    }
    if err := clock.Start(); err != nil {
        panic(err)
    }

    time.Sleep(2500 * time.Millisecond) // 供观察两拍

    _ = clock.Stop()
    if err := tickerHub.Stop(); err != nil {
        fmt.Println(err)
    }
    if err := watcherHub.Stop(); err != nil { // 排空推送队列并断开会话
        fmt.Println(err)
    }
}
```

`SnapshotTicker` には `Start` / `Stop` がなく、`metronome.Hub` は純粋な ticker として拍の配信に参加させるだけです。その sink が起動済みかは組み立て側が保証します——`pkg/assembly` は先に `watcherHub.Start()`（内部で `PushWatcher` を起動）し、その後に `tickerHub.Start()` と `clock.Start()` を呼びます。

## 注意事項

- **投影はディープコピーを行いません**：`toStatusSnapshot` は `PlotMetrics.UpstreamCounts` / `DownstreamCounts` を直接参照します。`scarecrow.MetricsWatcher` では安全です（毎回新しい map を返すため）が、カスタムの `MetricsView` が辞書を再利用する場合、sink はデータを保持する前に自身でコピーする必要があります——`PushWatcher.pushSnapshotAt` は `copySnapshot` でこれを実現しています。
- **時刻の零値の意味**：`StartTime` / `EndTime` の `time.Time` 零値は負の Unix タイムスタンプではなく `0` に投影されます。フロントエンドはこれに基づいて「未開始／未終了」を判定します。
- **状態は整数のスナップショット**：`PlotStatus` の `String()`（`not_started`/`running`/`stopped`）はペイロードに現れず、Web 側は `0/1/2` の数値に依存します。
- **エラーはラップされません**：`OnTick` が返すのは sink の error そのものなので、`errors.Is` が直接命中します。上位層で文脈を付けたければ自身でラップする必要があります。
- **ローカルでの重複判定なし**：等価なスナップショットが毎拍キューに入るため、サーバーが内容で重複判定します（そのため `elapsed_time` は意図的に除外されています）。高頻度の周期 + 大きなグラフはキューの負荷を増幅し、`DefaultPushBufferSize = 64` が唯一のバッファ上限です。
- **ノード集合は検証しません**：空のグラフは空の `snapshot: {}` をプッシュします。一度も起動していない Plot も、シードや上流イベントを受け取ったことで `MetricsWatcher` に現れることがあります（状態は `0`）。
- **`GetPlots` の所要時間は拍の goroutine 内**：指標の読み取りロックを保持する時間はノード数に比例します。指標の書き込みが頻繁（大量のシードイベント）な場合、スナップショットの読み取りと同じ `RWMutex` を巡って競合します。
