# pkg/farm/farm.go

> 📅 最終更新日: 2026/11/26

## 役割

`farm` パッケージは CelestialGrow の「グラフレベル」スケジューラであり、`farm.go` は `Farm` 型を提供する。複数の `plot.PlotNode` ノードを静的な有向グラフへ編成し、以下を担う：

- ノードの登録と名称の一意性検証
- グループ間（ハイパーエッジ）方式の全結合の確立
- Farm レベルのオブザーバー登録。**階層 Hub** を通じて Farm イベントとすべての Plot イベントを同時に受信する
- Farm レベルの節拍登録：各 Plot の ticker Hub を Farm の ticker Hub に収め、ユーザーによる ticker の追加も許可する
- 実行期リソースの組み立て（`assembly.StartLocalResources` / `StartReportResources` がオブザーバー、節拍 Hub、クロックの作成 / 起動 / 回収を一元化）
- 実行前のグラフ構造レンダリング（`getStructureList` → `RenderStructureList`）を `FarmStartEvent` へ書き込む
- ソースノードの検出（`SourceNodes`）、初期 seed の注入、終了シグナルの注入と終了待機
- リモート報告モード：セッション識別子を補完し、Farm を `reporter.InjectionTarget` として注入 ticker に渡す

`Farm` はオーケストレーション層のみを管理し、Plot のジェネリックな seed / fruit の型には関与しない——これは `plot.PlotNode` インターフェースが消去している。グラフアルゴリズム自体は同じディレクトリの `graph.go` に、構造レンダリングは `render.go` にあり、詳細は [`graph.md`](./graph.md) と [`render.md`](./render.md) を参照。

## 中核オブジェクト

### `Farm` 構造体

```go
type Farm struct {
    name        string
    plots       map[string]plot.PlotNode
    sourceNodes []string
    *OrderGraph

    eventClient runtime.EventClient
    watcherHub  *scarecrow.Hub
    tickerHub   *metronome.Hub
}
```

| フィールド | 役割 |
| --- | --- |
| `name` | Farm 名。`FarmStartEvent` / `FarmEndEvent` とログに書き込まれる；報告セッション識別子が空の場合、既定の `SessionID` 生成にも使われる |
| `plots` | plot 名で索引されるノード表。登録検証と検索に用いる |
| `sourceNodes` | `Run` / `Start` 開始時に一度だけ計算される「ソースノード代表」。各 Source SCC から 1 つ取る |
| `*OrderGraph` | 埋め込まれた有向グラフ。plot 間のエッジを記録し、トポロジーと SCC 分析に用いる |
| `eventClient` | 共有されるランタイムイベントクライアント。`AddPlot` によって各 plot に注入される |
| `watcherHub` | Farm レベルのオブザーバー Hub。各 Plot に登録されるネスト Hub でもあり、Farm と Plot のイベントを同時に受信する |
| `tickerHub` | Farm レベルの節拍 Hub。実行前に各 Plot の ticker Hub を収め、節拍ツリー全体を形成する |

> `Farm` は spout / inlet / 永続化オブザーバーやクロックを保持しない。オブザーバーとクロックは `pkg/assembly` が組み立て、Farm はオブザーバー Hub と節拍 Hub だけを公開する。`NewFarm` が `metronome.Hub` の作成に失敗した場合は直接 `panic` する。

### `PlotNode` インターフェース契約

`Farm` は `plot.PlotNode` インターフェースを通じて具体的なノードと疎結合になっている。このインターフェースは `pkg/plot/plot.go` に定義されており、`Farm` は実際に以下のメソッドを呼び出す：

| メソッド | 用途 |
| --- | --- |
| `GetName() string` | 一意な識別とエッジのマッチング |
| `GetState() int32` | ノード状態の読み取り（`0=idle` / `1=running` / `2=done`） |
| `GetMeta() scarecrow.NodeMeta` | 構築期のメタ情報（型名、tender 数など）を返し、`getNodeMeta` が `FarmStartEvent.NodeMeta` へ集約する |
| `GetSeedChanAny() any` | `any` として `seedChan` を公開し、上流の `ConnectTo` が型アサーションを行えるようにする |
| `ConnectTo(next PlotNode) error` | 型の互換性を検証し、下流の出力チャネルと上流名を登録する。型が一致しない場合は error を返す |
| `AddUpstream(name string)` | 上流の `ConnectTo` から呼ばれ、seal 集約のために上流名を登録する |
| `AddWatcher(listener scarecrow.Watcher) error` | オブザーバーを登録する。Farm は起動時に `watcherHub` をここへ登録する |
| `AddTicker(ticker metronome.Ticker) error` | 節拍を登録する。`Farm.AddTicker` はそのまま委譲する |
| `GetTickerHub() *metronome.Hub` | Plot 自身の節拍 Hub を返し、`injectTickerHubs` が Farm の ticker Hub へ収める |
| `SetEventClient(runtime.EventClient)` | `AddPlot` 時に同一のイベントクライアントを一括注入する |
| `StartAsync()` / `WaitAsync()` | 非同期ライフサイクル制御 |
| `SeedAny(seed any) error` | 初期 seed を注入する（`InjectTasks` が 1 つずつ呼ぶ） |
| `Seal()` | 外部の終了シグナルを注入する（`InjectTerminations` が呼ぶ） |

## 公開シンボル

| シンボル | シグネチャ | 用途 |
| --- | --- | --- |
| `NewFarm` | `func NewFarm(name string) *Farm` | Farm を構築する：plot 表、空のグラフ、ローカルイベントクライアント、オブザーバー Hub、節拍 Hub を初期化する |
| `PlotCount` | `func (f *Farm) PlotCount() int` | 登録済み plot の数を返す |
| `HasPlot` | `func (f *Farm) HasPlot(name string) bool` | 指定した plot が登録済みかを判定する |
| `GetPlot` | `func (f *Farm) GetPlot(name string) (plot.PlotNode, bool)` | 名前で plot を取得する。`ok` は存在有無を表す |
| `AddPlot` | `func (f *Farm) AddPlot(plots ...plot.PlotNode) error` | 1 つ以上の plot を登録する。非 nil、名称が非空かつ一意であることを要求する |
| `Connect` | `func (f *Farm) Connect(fromPlots, toPlots []plot.PlotNode) error` | ソースグループとターゲットグループの間に直積方式の接続を確立する |
| `AddWatcher` | `func (f *Farm) AddWatcher(watcher scarecrow.Watcher) error` | Farm レベルのオブザーバーを登録する（Farm イベントと Plot イベントを同時に受信する） |
| `AddTicker` | `func (f *Farm) AddTicker(ticker metronome.Ticker) error` | Farm レベルの ticker を登録し、Farm の実行期間全体の節拍を受信する |
| `Run` | `func (f *Farm) Run(inputs map[string][]any, logLevel string, report *reporter.Config) error` | 同期実行：起動 → seed 注入 → source の終了 → 終了待機；`report` はリモート報告設定 |
| `Start` | `func (f *Farm) Start(logLevel string, report *reporter.Config) error` | 同期実行だが注入は一切行わず、外部から駆動する；`report` はリモート報告設定 |
| `InjectTasks` | `func (f *Farm) InjectTasks(tasks map[string][]any) error` | 指定した Plot へ seed を注入する。実行時に呼び出せ、`reporter.InjectionTarget` も満たす |
| `InjectTerminations` | `func (f *Farm) InjectTerminations(nodes []string) error` | 指定した Plot へ終了シグナルを注入する。実行時に呼び出せ、`reporter.InjectionTarget` も満たす |

> `report` が `nil` の場合はローカル実行を意味し、ローカルリソースのみを組み立てる；非 `nil` の場合はさらにプッシュオブザーバー、スナップショット ticker、リモート注入 ticker を組み立てる。`reporter.Config` の `URL` は報告サービスのアドレスで、`SessionID` が空の場合は `normalizeReport` が `<Farm 名>@<UnixNano>` の形式で生成し、`SnapshotPeriod` / `InjectionPeriod` は節拍を単位とする（0 は既定周期を意味する）。

### `Run` / `Start` / `Inject*` の意味論の違い

| メソッド | Plot を起動するか | seed を注入するか | 終了シグナルを注入するか | ブロック | 典型的なシナリオ |
| --- | :---: | :---: | :---: | :---: | --- |
| `Run(inputs, logLevel, report)` | ✅ | ✅ `inputs` | ✅ `sourceNodes` | はい | 一括バッチ処理（リモート報告可） |
| `Start(logLevel, report)` | ✅ | ❌ | ❌ | はい | 外部駆動（reporter、対話的な注入など） |
| `InjectTasks(tasks)` | ❌ | ✅ `tasks` | ❌ | いいえ | 実行時の seed 追加 |
| `InjectTerminations(nodes)` | ❌ | ❌ | ✅ `nodes` | いいえ | 実行時の指定 Plot の終了 |

- `Run` = `validateRunInputs` + `start(logLevel, report, prepare)` であり、`prepare` は `InjectTasks(inputs)` と `InjectTerminations(f.sourceNodes)` を順に実行する。
- `Start` は入力検証を行わず、直接 `start(logLevel, report, nil)` を呼ぶ；**外部から終了シグナルが注入されない限り `Start` は返らない**。リモート報告モードでは、`InjectionTicker` が web 側で投入されたタスクと終了シグナルを `Start` 駆動の Farm に戻せる。
- `InjectTasks` / `InjectTerminations` は登録済みの Plot のみを操作し、リソースの組み立ても行わず、`sourceNodes` も変更しない。

> ログレベルは `NewFarm` では決まらない：`logLevel` は実行（`Run` / `Start`）ごとのパラメータであり、空文字列は `INFO` として扱われる。

## 主要フロー

### `AddPlot → Connect → Run` の全経路

```mermaid
sequenceDiagram
    autonumber
    participant U as 呼び出し元
    participant F as Farm
    participant A as assembly
    participant H as Farm watcherHub
    participant T as Farm tickerHub
    participant P as PlotNode

    U->>F: NewFarm(name)
    Note over F: plots 表 / OrderGraph / watcherHub / tickerHub を初期化

    U->>F: AddPlot(p1, p2, ...)
    loop 各 plot
        F->>F: 非 nil / 名称非空 / 一意性を検証
        F->>F: AddNode(name)
        F->>P: SetEventClient(eventClient)
    end

    U->>F: Connect(froms, tos)
    F->>F: uniquePlots で重複排除 + nil をフィルタ
    F->>F: requireRegistered(froms / tos)
    loop from × to の直積
        F->>P: from.ConnectTo(to)
        Note over P: yieldChans と upstreams を登録
        F->>F: AddEdge(from, to)
    end

    U->>F: Run(inputs, logLevel, report)
    F->>F: validateRunInputs(inputs)
    F->>F: sourceNodes = SourceNodes(OrderGraph)
    F->>F: normalizeReport(report)
    F->>F: injectTickerHubs()
    alt report == nil
        F->>A: StartLocalResources(watcherHub, tickerHub, logLevel, 1s)
    else report != nil
        F->>A: StartReportResources(watcherHub, tickerHub, F, logLevel, 1s, report)
    end
    A->>H: AddWatcher(Metrics / Lifecycle / Log、報告モードではさらに Push)
    A->>T: SnapshotTicker / InjectionTicker を登録（報告モードのみ）
    F->>H: emit(FarmStartEvent)
    loop 各 plot
        F->>P: AddWatcher(watcherHub)
    end
    loop 各 plot
        F->>P: StartAsync()
    end
    F->>F: InjectTasks(inputs)
    F->>F: InjectTerminations(sourceNodes)
    Note over F: 注入失敗 → フォールバック InjectTerminations(sourceNodes)
    loop 各 plot
        F->>P: WaitAsync()
    end
    F->>H: emit(FarmEndEvent)
    F->>A: stop()
```

### `start` の内部フロー（`Run` / `Start` 共通）

1. `startTime` を記録する。
2. `normalizeReport(report)`：`report` が `nil` または非空の `SessionID` を既に持つ場合はそのまま返す；それ以外はコピーして `<Farm 名>@<UnixNano>` を埋める。
3. `injectTickerHubs()`：各 Plot の `GetTickerHub()` を Farm の `tickerHub` に登録し、登録済みのものはスキップする。失敗時は直ちに返り、Plot はまだ起動していない。
4. `report` に応じて実行期リソースを組み立てる：
   - `nil` → `assembly.StartLocalResources(watcherHub, tickerHub, logLevel, metronome.DefaultInterval)`；
   - 非 `nil` → `assembly.StartReportResources(watcherHub, tickerHub, f, logLevel, metronome.DefaultInterval, report)`。Farm 自身が `InjectTasks` / `InjectTerminations` を実装しているため、`reporter.InjectionTarget` として利用できる。

    リソースには `MetricsWatcher`、`LifecycleWatcher`、`LogWatcher`（報告モードではさらに `PushWatcher`、`SnapshotTicker`、`InjectionTicker`）および節拍クロックが含まれる。失敗時は直ちに返り、Plot はまだ起動していない。
5. `defer stop()`：実行終了時に「クロック → tickerHub → watcherHub」の逆順で停止してフラッシュする；`StopFunc` は冪等である。
6. `scarecrow.FarmStartEvent` を発射する。`Farm`、`IsDAG`、`Plots`、`Edges`、`SourcePlots`、`NodeMeta`、`Structure`、`StartTime` を含む。
7. `defer` で `scarecrow.FarmEndEvent` を発射する（`Elapsed` は `time.Since(startTime)`）。
8. 各 plot について `plot.AddWatcher(f.watcherHub)` を呼ぶ——**Farm のオブザーバー Hub をネスト Hub として各 Plot へ登録する**。
9. 各 plot について `StartAsync()` を呼ぶ。
10. `prepare()` を呼ぶ（`Run` では注入ロジック、`Start` では `nil`）。
11. `prepare` がエラーを返した場合でも、フォールバックとして `InjectTerminations(f.sourceNodes)` を 1 回実行する（そのエラーは無視する）。終了シグナルを永遠に受け取らないグラフを待ち続けるのを避けるためである。
12. 各 plot について `WaitAsync()` を呼び、すべて終了した後に `prepareErr` を返す。

> `start` は組み立てエラーまたは注入エラーだけを返す；Plot 内部の育成失敗は `SeedWitherEvent` の形でオブザーバー体系に入り、`Run` / `Start` が error を返すことはない。

### 階層 Hub の配信

```mermaid
flowchart LR
    subgraph PlotHub[Plot 内部 Hub]
        PE[Plot.watcherHub.OnEvent] --> PH[watcherHub]
    end
    PH -->|AddWatcher で登録| FH[Farm watcherHub]
    FH --> M[MetricsWatcher]
    FH --> L[LifecycleWatcher]
    FH --> G[LogWatcher]
    FH --> PU[PushWatcher<br/>報告モードのみ]
    FH --> W[ユーザー AddWatcher]
    FE[Farm.emit] --> FH

    style FH fill:#eef
```

- Plot イベント（`PlotStart`、`SeedRipen` など）は Plot 自身の Hub が登録済みオブザーバーへ配信し、Farm のオブザーバー Hub もその 1 つであるため、さらに上位へ配信される。
- Farm イベント（`FarmStartEvent` / `FarmEndEvent`）は直接 Farm のオブザーバー Hub に入り、Plot へは**配信されない**。
- `scarecrow.Find` はネスト Hub をたどってオブザーバーを検索でき、`plot.Harvest()` はまさにこれによって Plot Hub → Farm Hub の連鎖上で `LifecycleWatcher` を見つける。
- `Hub` のコピーオンライトな意味論は実行時のオブザーバー追加を許すが、それは以降のイベントにのみ有効である；同じ Hub を重複登録すると配信が重複する。

節拍側も同様に 1 本のツリーになる：

```mermaid
flowchart LR
    C[metronome.Clock<br/>DefaultInterval=1s] --> FT[Farm tickerHub]
    FT --> PT1[Plot A tickerHub]
    FT --> PT2[Plot B tickerHub]
    FT --> ST[SnapshotTicker<br/>報告モードのみ]
    FT --> IT[InjectionTicker<br/>報告モードのみ]
    FT --> UT[ユーザー AddTicker]
```

- `injectTickerHubs` は Plot の ticker Hub を Farm の `tickerHub` の下にぶら下げるだけである；Farm 自身は Plot ticker を作成しない。
- `assembly` は `tickerHub` を起動する前にスナップショット / 注入 ticker を登録し、ツリー全体を駆動する唯一のクロックを作成する。
- Plot ticker と Farm ticker はいずれも自身の `TickPeriod` で節拍をフィルタし、周期の意味論は `pkg/metronome` が定義する。

### `sourceNodes` の計算

`Run` と `Start` は起動前にいずれも `f.sourceNodes = SourceNodes(f.OrderGraph)` を実行する：

1. `TarjanSCC` で強連結成分を求める；
2. 凝縮グラフにおいて入次数が 0 の Source SCC を計算する（`SourceSCCs`）；
3. 各 Source SCC から `scc[0]` を代表ノードとして取る（`SourceNodes`）。

これにより、source が強連結なサイクル内にあっても代表ノード 1 つにだけ終了シグナルを送り、seal の重複を避ける。孤立ノード（`AddPlot` 後もエッジがないもの）も同様に Source SCC となるため、終了シグナルが注入される。

## エラーシナリオ

### `AddPlot`

- いずれかの plot が `nil` → `plot is nil`
- 名称が空 → `plot name cannot be empty`
- 名称が重複 → `plot %q already exists`

最初のエラーで直ちに返る；それまでに正常に追加された plot は**ロールバックされない**。

### `Connect`

- 重複排除後に `fromPlots` が空 → `from plots cannot be empty`
- 重複排除後に `toPlots` が空 → `to plots cannot be empty`
- いずれかの plot が `nil` または未登録 → `plot is nil` / `plot %q is not registered in farm`
- `from.ConnectTo(to)` の型アサーションが失敗 → `plot %q yield type is incompatible with plot %q seed type` をそのまま伝播する

> ⚠️ **部分的な失敗は取り消されない**：内側のループは一度失敗すると直ちに返り、確立済みの接続（`yieldChans`、`upstreams`、グラフのエッジ）は残る。再度 `Connect` するとグラフ層では重複エッジとして無視されるが、Plot 内部のマッピングには古い状態が残る。

### `AddWatcher` / `AddTicker`

- `watcher` が `nil` → `scarecrow: cannot add nil listener`
- Hub の循環を形成 → `scarecrow: cyclic hub registration rejected: ...` / `scarecrow: cannot add a hub to itself`
- `ticker` が `nil` → `metronome: cannot add nil ticker`
- ticker の節拍周期が不正 → `metronome: tick period must be positive ...`
- ticker の重複登録または Hub の循環 → `metronome: ticker already registered` / `metronome: cyclic hub registration rejected: ...`

### `Run` / `Start`

| 段階 | エラー発生源 | 説明 |
| --- | --- | --- |
| 検証（`Run` のみ） | `validateRunInputs` | `inputs` に未登録の plot が存在 → `plot %q is not registered in farm`。この時点で**Plot は 1 つも起動していない** |
| 節拍の組み立て | `injectTickerHubs` | Plot ticker Hub の登録失敗（nil / 重複 / 循環）；Plot はまだ起動していない |
| リソースの組み立て | `assembly.StartLocalResources` / `StartReportResources` | Hub または ticker Hub が nil、オブザーバー / ticker の起動失敗、`report` 設定が不正；Plot はまだ起動していない |
| 登録 | `plot.AddWatcher(f.watcherHub)` | Hub の循環または nil のオブザーバー；`StartAsync` の前に返る |
| 注入 | `InjectTasks` | 未登録 plot、`SeedAny` の型アサーション失敗（`plot %q seed type mismatch: got %T`） |
| 注入 | `InjectTerminations` | 未登録 plot |
| 待機 | `WaitAsync` | エラーを返さない |

### `InjectTasks`

- まずすべての key に対して `validateRunInputs` を行う：いずれかのターゲットが未登録 → エラーを返し、**seed は 1 つも注入しない**。
- その後 map のイテレーション順に 1 つずつ `SeedAny` する；途中で型が一致しないものがあれば直ちに返り、それまでに注入した seed は**取り消せない**。

### `InjectTerminations`

- まずすべての名称が登録済みであることを検証する；いずれかが未登録 → エラーを返し、**終了シグナルは 1 つも注入しない**。
- 検証が通れば 1 つずつ `Seal()` を呼び、外部の終了シグナル（`Source == "__input__"`）を注入する。強い終了の意味論を持ち、まだ到達していない上流の seal を待たない。
- ある Plot へ終了シグナルを注入した後、その Plot へ seed を注入しても処理されない。

## 重要な詳細

### グループ間の直積

`Connect(froms, tos)` はまず両端に対して `uniquePlots`（重複排除 + `nil` のフィルタリング）を行い、その後 `len(froms) × len(tos)` 本のエッジを確立する：

```go
farm.Connect([]plot.PlotNode{root}, []plot.PlotNode{midA, midB})
// 等价于 root → midA 与 root → midB 两条独立边
```

「ソースグループ × ターゲットグループ」の間だけにエッジが張られ、同一グループ内のノード同士は接続されない。

### 注入失敗時の終了フォールバック

`start` では `prepare()` が失敗した後でも `InjectTerminations(f.sourceNodes)` を呼ぶ：

- 終了シグナルのないグラフで `WaitAsync` が永久にブロックするのを避ける；
- フォールバックのエラーは明示的に無視され（`_ =`）、戻り値は元の `prepareErr` のままである；
- このフォールバックは `sourceNodes` だけを終了させ、下流は seal シグナルを受け取った後に通常の伝播経路で終了する。

### イベントとログ

- `Farm` は 2 つのイベントだけを発射する：`FarmStartEvent`、`FarmEndEvent`。これらは `pkg/scarecrow/event.go` に定義されている。
- `Structure` フィールドは `getStructureList()` に由来する。すなわち `RenderStructureList(f.Nodes(), f.OutEdges(), f.sourceNodes)` である；`NodeMeta` は `getNodeMeta()` に由来し、Plot 名ごとに `GetMeta()` を集約する。
- `persist.LogWatcher` は `FarmStartEvent` を消費するときに構造を 1 行ずつ出力し、`FarmEndEvent` を消費するときに所要時間を出力する。
- 報告モードでは、`reporter.PushWatcher` が `FarmStartEvent` を消費してグラフのメタ情報をプッシュし、`SeedWitherEvent` を消費してエラーをプッシュし、`FarmEndEvent` を消費してまず最終状態スナップショットを追加送信してからセッションのオフラインを通知する；周期スナップショットは `SnapshotTicker` が駆動する。
- `Farm` 自身はデータベースへ書き込まない；ライフサイクル、ログ、リモート報告はすべて `assembly` が登録したオブザーバーが行う。

### `pkg/runtime` との協調

`eventClient` は `AddPlot` 時に注入され、すべての plot が同一のイベント ID 名前空間を共有するため、plot をまたぐ seed → fruit → seal のイベント連鎖を追跡できる。

### セッション識別子と報告

- `normalizeReport` は空の `SessionID` を補完するだけで、呼び出し元が渡した設定を変更しない（先に構造体をコピーする）。
- 生成形式は `fmt.Sprintf("%s@%d", f.name, time.Now().UnixNano())` で、同一 Farm の複数回の実行がサーバー側で互いに上書きされないことを保証する。
- `StartReportResources` は Farm を注入ターゲットとして扱い、周期的に `/api/pull_injection` を取得して `tasks` / `terminations` を `InjectTasks` / `InjectTerminations` に戻す。

### 並行制御

- `Run` / `Start` は**同期**呼び出しである：順に `StartAsync` した後、`WaitAsync` ですべての plot の終了を待つ。
- `StartAsync` の内部では `sync.WaitGroup.Go`（Go 1.25 の書き方）を用いて各 plot の `sprout` スケジューラを起動する。
- `InjectTasks` / `InjectTerminations` は `Start` がブロックしている間に他のコルーチンから呼べるよう設計されている；`Farm` の `plots` 表自体はロックしないため、**登録 / 接続の段階を実行段階と並行させることはできない**。
- `watcherHub` / `tickerHub` 自身はスレッドセーフだが、`injectTickerHubs` は起動前に 1 回だけ呼ばれる。

## 使用例

### バッチ処理：`Run`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

func main() {
    double := plot.NewSinglePlot("double", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2))

    format := plot.NewSinglePlot("format", func(seed int) (string, error) {
        return fmt.Sprintf("result=%d", seed), nil
    })

    f := farm.NewFarm("demo_farm")
    if err := f.AddPlot(double, format); err != nil {
        panic(err)
    }
    if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{format}); err != nil {
        panic(err)
    }

    if err := f.Run(map[string][]any{
        "double": {1, 2, 3, 4},
    }, "INFO", nil); err != nil {
        panic(err)
    }
}
```

実行状態を `celestialflow-web` へプッシュする必要がある場合は、報告設定を渡す（`github.com/Mr-xiaotian/CelestialGrow/pkg/reporter` を追加でインポートする）；`SessionID` は空のままで自動生成される：

```go
if err := f.Run(map[string][]any{
    "double": {1, 2, 3, 4},
}, "INFO", &reporter.Config{
    URL: "http://127.0.0.1:5005",
}); err != nil {
    panic(err)
}
```

### 外部駆動：`Start` + `InjectTasks` / `InjectTerminations`

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

func main() {
    root := plot.NewSinglePlot("root", func(seed int) (int, error) {
        return seed * 2, nil
    }, plot.WithTenders(2), plot.WithChanSize(8))

    f := farm.NewFarm("external_driven")
    if err := f.AddPlot(root); err != nil {
        panic(err)
    }

    if err := f.AddWatcher(scarecrow.WatcherFunc(func(event scarecrow.Event) error {
        switch ev := event.(type) {
        case scarecrow.PlotStartEvent:
            fmt.Println("plot started:", ev.Plot)
        case scarecrow.FarmEndEvent:
            fmt.Println("farm done in", ev.Elapsed)
        }
        return nil
    })); err != nil {
        panic(err)
    }

    go func() {
        for i := 0; i < 3; i++ {
            if err := f.InjectTasks(map[string][]any{"root": {i}}); err != nil {
                panic(err)
            }
        }
        if err := f.InjectTerminations([]string{"root"}); err != nil {
            panic(err)
        }
    }()

    if err := f.Start("INFO", nil); err != nil {
        panic(err)
    }
}
```

> `Start` はすべての Plot が終了するまでブロックする；外部のコルーチン（またはシグナル処理ロジック）が最終的に `InjectTerminations` を呼ぶことを保証しなければ、呼び出しは返らない。非 nil の `report` を渡した場合、リモート注入 ticker も `InjectTasks` / `InjectTerminations` を駆動する。

### 実行終了後に結果を読む

```go
records, err := root.Harvest()
if err != nil {
    panic(err)
}
for _, record := range records {
    fmt.Println(record.Status) // 例如 ripen
}
```

`Harvest` は Farm のオブザーバー Hub が Plot Hub へ登録済みであることに依存し、それによってネスト Hub をたどって `LifecycleWatcher` を見つけられる。

## 注意事項

- **再入不可**：同一の `Farm` インスタンスは `Run` / `Start` の並行実行や繰り返し実行をサポートしない。`start` のたびに `watcherHub` が各 Plot へ再登録され、イベント配信が重複する；Plot の `state` も `done` のままである。
- **注入順序**：`InjectTasks` は `plots[name]` が登録済みであることに依存する。実行時の注入前に登録表を変更しないこと。
- **終了は不可逆**：`Seal()` の後、その Plot の `sprout` は seed を消費しなくなる；`InjectTasks` はエラーを返さないが、seed は `seedChan` に滞留し、バッファが満杯になると呼び出しがブロックされる。
- **グラフとノード集合**：`OrderGraph` のノードは `AddEdge` によって自動補完されることがあるが、通常の経路では `AddPlot` がすべてのノードを登録済みである；`sourceNodes` は `plots` のビューではなくグラフのビューを用いる。
- **ログレベル**：`logLevel` の値は `pkg/persist` のレベル表を参照し、未知の値は `INFO` として扱われる。
- **報告と注入**：`report == nil` のときは `PushWatcher` / `SnapshotTicker` / `InjectionTicker` を組み立てず、いかなる web サービスにもアクセスしない；`report != nil` のときは `URL` が正当な `http(s)` アドレスでなければならず、そうでない場合はリソース組み立て段階でエラーが返る。
- **テストカバレッジ**：`farm_connect_test.go`、`farm_start_test.go`、`farm_structure_test.go`、`farm_route_test.go`、`farm_split_test.go`、`farm_inject_test.go`、`farm_harvest_test.go`、`farm_report_test.go`、`graph_test.go`、`render_test.go` が、登録 / 接続、Run の線形フロー、非自明なトポロジー、ルーティング / 分割、外部注入、Harvest、リモート報告、グラフアルゴリズムと構造レンダリングをカバーする。
