# pkg/reporter/config.go

> 📅 最終更新日: 2026/11/26

`pkg/reporter/config.go` は、リモート報告の実行に必要な最小構成のキャリア `Config` を定義します。それ自体は純粋なデータです：検証もメソッドも持たず、実際の妥当性チェックは `NewPushWatcher`、`NewSnapshotTicker`、`NewInjectionTicker` の 3 つのコンストラクタで行われます。

## 役割

- 「報告サービスアドレス + セッション識別子 + 2 つの拍周期」をまとめて `Farm.Run` / `Farm.Start` に渡します。
- `pkg/api` が外部に公開する `Report` 型（`type Report = reporter.Config`、**型エイリアス**であり新しい型ではありません）として機能し、呼び出し側は `pkg/reporter` を直接 import する必要がありません。
- `pkg/assembly.StartReportResources` が読み取り、プッシュウォッチャー、スナップショット ticker、注入 ticker をそれぞれ構築します。

`report` 引数が `nil` の場合は**ローカル実行**を意味します：`Farm` は `assembly.StartLocalResources` を通り、プッシュも注入の取得も行いません。したがって `Config` は「リモート報告を有効にするかどうか」のスイッチでもあります。

## 主要オブジェクト

### `type Config`

```go
type Config struct {
    URL             string // 上报服务基础地址
    SessionID       string // 上报会话标识；为空时由运行入口生成
    SnapshotPeriod  int    // 状态快照节拍周期
    InjectionPeriod int    // 注入拉取节拍周期
}
```

| フィールド | 型 | `0` 値の意味 | 実際の利用側 |
|------|------|-----------|-----------|
| `URL` | `string` | **不正**：`normalizeBaseURL` が `reporter: base url is empty` を返す | `NewPushWatcher`、`NewInjectionTicker` |
| `SessionID` | `string` | **入口が生成**：`Farm.normalizeReport` が `<farmName>@<UnixNano>` を生成 | プッシュペイロードの `session_id`、注入リクエストのクエリパラメータ |
| `SnapshotPeriod` | `int` | `DefaultSnapshotPeriod`（`1`、すなわち毎拍）を使用 | `NewSnapshotTicker` |
| `InjectionPeriod` | `int` | `DefaultInjectionPeriod`（`5`）を使用 | `NewInjectionTicker` |

すべてのフィールドがエクスポートされており、`Config` はリテラルで直接構築できます。コンストラクタも、既定値を埋めるメソッドも、`Validate` もありません。

### 周期の単位は「秒」ではなく「拍」

`SnapshotPeriod` / `InjectionPeriod` は `metronome.Periodic` の意味における拍数です：`1` は毎拍発火、`2` は 2 拍ごとに 1 回発火（`(Seq-1)%period == 0`）を意味します。実際のウォールクロック間隔 = 拍数 × クロック基準周期です。`Farm` の組み立て時には `metronome.DefaultInterval`（`1s`）が固定で渡されるため、既定値はおおよそ「スナップショット 1 秒に 1 回、注入 5 秒に 1 回」と理解できます。呼び出し側が `metronome.Clock` の間隔を 200ms に変更すれば、同じ設定が 200ms / 1s になります。

負の周期はコンストラクタでエラーになります（`snapshot period must be positive` / `injection period must be positive`）。`0` だけが「既定値を使う」を表します。

## 主要フロー

```mermaid
flowchart LR
    U[呼び出し側] -->|&reporter.Config| F[Farm.Run / Farm.Start]
    F -->|SessionID が空| N[normalizeReport<br/>farm@UnixNano を生成]
    N --> A[assembly.StartReportResources]
    F -->|非 nil| A
    A --> W[NewPushWatcher<br/>report.URL / SessionID]
    A --> S[NewSnapshotTicker<br/>report.SnapshotPeriod]
    A --> I[NewInjectionTicker<br/>report.URL / InjectionPeriod / timeout=0]
```

1. `Farm.Run` / `Farm.Start` はまず `normalizeReport` を呼びます：`report != nil` かつ `SessionID` が空白除去後に空の場合に限り、設定を**コピー**して `<farmName>@<time.Now().UnixNano()>` を埋めます。元のオブジェクトは変更されません。
2. `assembly` は同じ `Config` で 3 つのコンポーネントを構築します。`NewInjectionTicker` の `timeout` には `0` が渡され、`DefaultPullTimeout`（3 秒）が採用されます。
3. いずれかのコンストラクタがエラーを返すと、`assembly` は直ちにエラーを返し、`Farm` は今回の実行を中止します（黙ってローカル実行に退避することはありません）。

## 使用例

### バッチ処理 + リモート報告

```go
package main

import (
    "fmt"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
)

func main() {
    double := plot.NewSinglePlot("double", func(seed int) (int, error) {
        return seed * 2, nil
    })
    format := plot.NewSinglePlot("format", func(seed int) (string, error) {
        return fmt.Sprintf("result=%d", seed), nil
    })

    f := farm.NewFarm("demo")
    if err := f.AddPlot(double, format); err != nil {
        panic(err)
    }
    if err := f.Connect([]plot.PlotNode{double}, []plot.PlotNode{format}); err != nil {
        panic(err)
    }

    report := &reporter.Config{
        URL:             "http://127.0.0.1:5005",
        SessionID:       "demo@manual", // 留空则由 Farm 生成 demo@<UnixNano>
        SnapshotPeriod:  2,             // 每 2 拍推一次状态
        InjectionPeriod: 5,             // 每 5 拍拉一次注入
    }
    if err := f.Run(map[string][]any{"double": {1, 2, 3}}, "INFO", report); err != nil {
        panic(err)
    }
}
```

### 外部駆動 + リモート注入

`Start` は初期タスクと終了シグナルを注入しないため、reporter の `/api/pull_injection` から駆動するのに適しています。`SessionID` を明示的に指定する（または空のままにして `Farm` に生成させる）必要があり、そうしないと `NewPushWatcher` が `session id is empty` で失敗します。

```go
package main

import (
    "github.com/Mr-xiaotian/CelestialGrow/pkg/farm"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
)

func main() {
    root := plot.NewSinglePlot("root", func(seed int) (int, error) { return seed, nil })
    f := farm.NewFarm("external")
    if err := f.AddPlot(root); err != nil {
        panic(err)
    }

    // 阻塞直到远程注入终止信号（或服务端下发 shutdown）。
    if err := f.Start("INFO", &reporter.Config{
        URL:             "http://127.0.0.1:5005",
        InjectionPeriod: 1,
    }); err != nil {
        panic(err)
    }
}
```

## 注意事項

- **`URL` は `Config` の構築時には検証されません**：エラーは `Farm` の起動段階まで遅れて表面化します。`URL` は `http` / `https` のみを受け付け、末尾の `/` は除去されます。パス付きのアドレス（例 `http://host/api`）は検証を通過しますが、以降のエンドポイントは**文字列連結**なので、`http://host/api/api/push_snapshot` のような二重プレフィックスのアドレスになります。
- **`SessionID` はサーバー側の一意キーです**：空の場合は `Farm` が `UnixNano` で生成し、異なる実行が互いを上書きしないことを保証します。手動指定時に 2 回の実行で同じ値を使い回すと、サーバー側の状態が互いに干渉します。
- **`SnapshotPeriod` と `InjectionPeriod` の既定値は異なります**（1 対 5）：スナップショットは量が多いが安価（キューに入れるだけ）であり、注入の取得は 1 回の同期 HTTP リクエストなので、既定ではより疎になっています。
- **`Config` はタイムアウトを含みません**：プッシュの HTTP タイムアウト（`DefaultPushTimeout`）、1 回のエンキュー タイムアウト（`DefaultSendTimeout`）、注入取得のタイムアウト（`DefaultPullTimeout`）はいずれもパッケージ内の定数で、現在設定の入口はありません。
- **並行安全性**：`Config` は読み取り専用のデータキャリアです。`Farm.normalizeReport` は `SessionID` が空の場合に構造体をコピーするため、呼び出し側が同じ `Config` を後で使い回してもセッションが混ざることはありません。
