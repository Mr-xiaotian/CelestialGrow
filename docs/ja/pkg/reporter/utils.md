# pkg/reporter/utils.go

> 📅 最終更新日: 2026/11/26

`pkg/reporter/utils.go` は reporter パッケージ内部の投影、ディープコピー、URL 検証、HTTP クライアント、シリアライズの補助関数を集めています。すべて**非公開**ですが、サーバーへプッシュした後の JSON の形（空集合は `null` ではなく `[]`/`{}`）、タイムスタンプの精度、失敗時の降格挙動を決定します。

## 役割

| 関数 | 一言の責務 | 主な呼び出し元 |
|------|-----------|-----------|
| `toStatusSnapshot` | 指標 → `NodeStatus` の投影 | `SnapshotTicker.OnTick`、`PushWatcher.OnEvent(FarmEnd)` |
| `copyEdges` | 隣接リストのディープコピー、空の出辺は `[]` にエンコード | `PushWatcher.OnEvent(FarmStart)` |
| `copySnapshot` | 状態スナップショットとそのカウント辞書のディープコピー | `PushWatcher.pushSnapshotAt` |
| `copyIntMap` | `map[string]int` のディープコピー、空のマップは `{}` にエンコード | `copySnapshot`、テストプローブ |
| `copyNodeMeta` | ノードメタ情報テーブルのコピー | `PushWatcher.OnEvent(FarmStart)` |
| `newHTTPClient` | 独立した Transport を保持するクライアントを作成 | `pushHandler.BeforeStart`、`InjectionTicker.Start` |
| `normalizeBaseURL` | 報告の基底アドレスを検証・正規化 | `NewPushWatcher`、`NewInjectionTicker` |
| `unixSeconds` | `time.Time` → Unix 浮動小数点秒 | スナップショット／グラフメタ／エラーレコードのタイムスタンプ |
| `toJSONValue` | 任意のタスクペイロードが JSON エンコード可能であることを保証 | `PushWatcher.OnEvent(SeedWither)` |

## 主要関数

### `toStatusSnapshot`

```go
func toStatusSnapshot(metrics map[string]scarecrow.PlotMetrics) map[string]NodeStatus
```

- 出力 map の容量は `len(metrics)` で事前確保され、零値の指標を含む**すべてのノードキーを保持**します。
- `Status` は `int(PlotStatus)` に変換され（`0/1/2`）、文字列は出力しません。
- `StartTime` / `EndTime`：`time.Time` の零値は `0.0`、それ以外は `unixSeconds(...)`。これにより「未開始／未終了」が 1970 年より前の負数ではなく、明確なセンチネル値として Web 側に現れます。
- カウントは直接マッピング：`InputTotal / Processed / Pending / Succeeded / Failed / Skipped`。
- `UpstreamCounts` / `DownstreamCounts` は**参照渡し**で、コピーしません。
- 派生した所要時間（`elapsed` など）は意図的に出力しません：毎拍変化し、サーバー側の内容による重複判定を壊してしまいます。

> 参照セマンティクスの安全性：`scarecrow.MetricsWatcher.GetPlots` は毎回新しい辞書を返すため、そのまま参照できます。`PushWatcher.pushSnapshotAt` はさらに `copySnapshot` を通すので、エンキュー後のデータは呼び出し側と完全に分離されます。カスタムの `MetricsView` が辞書を再利用する場合は、自身で不変性を保証するか、sink 側でコピーする必要があります。

### `copyEdges`

```go
func copyEdges(edges map[string][]string) map[string][]string
```

```go
result[from] = append([]string{}, targets...)
```

- `append([]string{}, nil...)` は**非 nil の空スライス**を生成するため、JSON では `[]` にエンコードされます。`targets` を直接代入すると、Go は nil スライスを `null` にエンコードするため、Web 側で追加の互換処理が必要になります。
- 同時に各スライスの基底配列もコピーするため、エンキュー後に呼び出し側が元の隣接リストを変更しても、エンキュー済みのペイロードには影響しません。
- `edges == nil` のときは**非 nil** の空 map を返し（`make(map[string][]string, len(edges))`、容量 0）、`{}` にエンコードされます。

### `copySnapshot`

```go
func copySnapshot(snapshot map[string]NodeStatus) map[string]NodeStatus
```

- 各 `NodeStatus` の値（構造体自体は値型）をコピーし、その中の `UpstreamCounts`、`DownstreamCounts` に対して `copyIntMap` を呼びます。
- 目的：`Inlet.Send` が返った後も呼び出し側は元の map／辞書を保持して変更する可能性があるため、エンキューするペイロードは呼び出し側と切り離されていなければなりません。
- 空 map の入力は非 nil の空 map を返し、JSON は `{}` になります。

### `copyIntMap`

```go
func copyIntMap(source map[string]int) map[string]int
```

- `make` で非 nil の結果を作ってから項目ごとにコピーするため、`nil` 入力でも非 nil の空 map になり、`null` ではなく `{}` にエンコードされます。
- これが、スナップショット内の 2 つのカウント辞書に `null` が決して現れない根本的な保証です。

### `copyNodeMeta`

```go
func copyNodeMeta(meta map[string]scarecrow.NodeMeta) map[string]scarecrow.NodeMeta
```

- map 自体だけをコピーします（浅いコピー）。`scarecrow.NodeMeta` の 3 つのフィールド（`ClassName`、`ExecutionMode`、`MaxWorkers`）はいずれも値型で、スライス／マップ／ポインタを含まないため、浅いコピーで完全に分離されます。
- nil 入力は非 nil の空 map を返し、`{}` にエンコードされます。

### `newHTTPClient`

```go
func newHTTPClient(timeout time.Duration) *http.Client
```

```go
transport := http.DefaultTransport
if base, ok := transport.(*http.Transport); ok {
    transport = base.Clone()
}
return &http.Client{Timeout: timeout, Transport: transport}
```

- `http.DefaultTransport` をクローンすることで、各 reporter コンポーネントが**独立した接続プール**を持ちます：`Stop` 内の `CloseIdleConnections()` は自分のアイドル接続だけを閉じ、プロセス内の他の HTTP クライアントには影響しません。
- クローンは作成時に行われるため、その後 `http.DefaultTransport` のフィールド（`MaxIdleConns` など）を変更しても、作成済みのクライアントには影響しません。
- プロセスが `http.DefaultTransport` を `*http.Transport` 以外の実装に置き換えている場合、そのインスタンスをそのまま共有するため、`CloseIdleConnections` の分離性は成立しません（`*http.Client.CloseIdleConnections` は Transport のオプションのインターフェースを呼びます）。
- `timeout` はリクエスト全体（接続 + リダイレクト + body の読み取り）の上限として直接使われます。`0` はタイムアウトなしを意味しますが、本番の呼び出し側は 0 を渡しません：`pushHandler` は `DefaultPushTimeout` を、`InjectionTicker` は構築引数を使います（0 は `DefaultPullTimeout` に置換済み）。

### `normalizeBaseURL`

```go
func normalizeBaseURL(raw string) (string, error)
```

処理手順とエラー：

| 手順 | 動作 | 失敗時のエラー |
|:----:|------|---------|
| 1 | `strings.TrimSpace(raw)`、続いて**すべての**末尾 `/` を除去 | 結果が空 → `reporter: base url is empty` |
| 2 | `url.Parse`、`Scheme != ""` かつ `Host != ""` を要求 | `reporter: invalid base url %q`（引用符内は**元の** `raw`） |
| 3 | scheme は `http` または `https` でなければならない | `reporter: base url must use http or https, got %q` |
| 4 | 正規化後の文字列を返す | — |

要点：

- `TrimRight(..., "/")` は連続する複数のスラッシュを除去します：`http://host///` → `http://host`。
- 戻り値は**トリム後の元の文字列**で、`url.String()` による再整形は行わないため、大文字小文字、パス、クエリパラメータはそのまま保持されます。
- 「scheme がある + host がある + プロトコルが http(s)」だけを検証し、**パスは検証しません**。エンドポイントは `baseURL + "/api/..."` の文字列連結で作られるため、パス付きの基底アドレス（`http://host/api`）は `/api/api/...` の二重プレフィックスを生みます。クエリ文字列付きのアドレスはパスの前にクエリが挿入され、結果は予期できません。基底アドレスは host[:port] までだけを書くべきです。
- 到達可能性、ポートの妥当性、平文 HTTP の制限はいずれも確認しません。TLS の検証は完全に `http.Transport` に委ねられます。
- ユーザー情報（`http://user:pass@host`）は保持されます——`Host` フィールドに userinfo は含まれませんが、戻り値は元の文字列のため、認証情報はリクエストとともに送信されます。

### `unixSeconds`

```go
func unixSeconds(at time.Time) float64
```

```go
float64(at.Unix()) + float64(at.Nanosecond())/1e9
```

- **サブ秒精度付き**の Unix 秒を出力します（例 `time.Unix(10, 500_000_000)` → `10.5`）。celestialflow-web の `number` タイムスタンプ契約と一致します。
- `Unix()` は負の時刻（1970 年より前）では切り捨て、`Nanosecond()` は常に非負なので、この式は負の時刻でも成立します。ただし reporter の呼び出し箇所はいずれも零値の時刻を事前に排除します。
- 零値の `time.Time` に対して直接呼ぶと約 `-6.2e10` になり、`0` にはなりません——`toStatusSnapshot` は `IsZero()` でガードし、`PushWatcher` のタイムスタンプはすべて実際のイベント／`time.Now()` に由来します。

### `toJSONValue`

```go
func toJSONValue(value any) any
```

- まず `json.Marshal(value)` で**シリアライズ可能性のプローブ**を行い、成功すればそのまま返します（元の型を保持し、数値や構造が文字列に変換されることはありません）。
- 失敗した場合は `fmt.Sprintf("%+v", value)` の可読文字列を返します。
- 設計意図：`SeedWitherEvent.Seed` は `chan`、`func`、`complex`、`NaN`/`Inf`、循環参照などエンコード不能な値を含む可能性があります。そのまま `taskFailRecord` に入れると、`json.Marshal` が**エラー報告全体**を失敗させます。降格後も少なくともエラーの診断可能性は保たれます。
- 降格テキストは不安定（チャネルや関数はアドレスを出力）で、表示にのみ適し、リプレイの入力には適しません。

## 主要フロー

```mermaid
flowchart TD
    PM[map string PlotMetrics] --> P[toStatusSnapshot]
    P -->|NodeStatus| CS[copySnapshot]
    CS -->|copyIntMap ×2| Q[エンキューするペイロード]
    FE[FarmStartEvent.Edges] --> CE[copyEdges]
    CE -->|空スライスは非 nil| Q2[graphMetaRecord]
    NM[FarmStartEvent.NodeMeta] --> CN[copyNodeMeta]
    CN --> Q2
    RAW[raw base url] --> N[normalizeBaseURL]
    N -->|scheme/host を検証| C[newHTTPClient]
    C -->|Transport をクローン| HTTP[POST / GET]
    T[time.Time] --> U[unixSeconds]
    U --> Q
    U --> Q2
    ANY[任意の Seed] --> J[toJSONValue]
    J --> Q3[taskFailRecord]
```

## 使用例

次のプログラムは公開 API を通じて 3 つのことを間接的に検証します：末尾の `/` が正規化されること、`nil` のカウント辞書が `{}` にエンコードされること、`nil` の隣接リストが `[]` にエンコードされること。

```go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/reporter"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// demoMetrics 返回一个零值节点，counts 故意留 nil。
type demoMetrics struct{}

func (demoMetrics) GetPlots() map[string]scarecrow.PlotMetrics {
    return map[string]scarecrow.PlotMetrics{"root": {}}
}

func main() {
    var bodies []string
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        bodies = append(bodies, string(body))
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    // 故意带尾部斜杠：normalizeBaseURL 会去掉它。
    watcher, err := reporter.NewPushWatcher("demo@1", server.URL+"/", demoMetrics{})
    if err != nil {
        panic(err)
    }
    if err := watcher.Start(); err != nil {
        panic(err)
    }

    // Edges["leaf"] 为 nil：copyEdges 会编码成 []。
    if err := watcher.OnEvent(scarecrow.FarmStartEvent{
        Farm:  "demo",
        Plots: []string{"root", "leaf"},
        Edges: map[string][]string{"root": {"leaf"}, "leaf": nil},
        StartTime: time.Unix(10, 0),
    }); err != nil {
        panic(err)
    }

    // NodeStatus 的 counts 留 nil：copyIntMap 会编码成 {}。
    if err := watcher.PushSnapshot(map[string]reporter.NodeStatus{"root": {}}); err != nil {
        panic(err)
    }

    if err := watcher.Stop(); err != nil {
        panic(err)
    }

    for _, body := range bodies { // bodies[0] 图元，bodies[1] 快照
        var decoded map[string]any
        if err := json.Unmarshal([]byte(body), &decoded); err != nil {
            panic(err)
        }
        fmt.Println(decoded["edges"], decoded["snapshot"])
    }
}
```

出力では `edges` に `"leaf":[]` が含まれ、`snapshot.root` に `"upstream_counts":{}` と `"downstream_counts":{}` が含まれるはずです——これが utils 層の空集合保証です。

## 注意事項

- **空集合の約束はハードな契約です**：`copyEdges` / `copyIntMap` / `copySnapshot` はいずれも意図的に `make` で非 nil の空集合を返します。これを直接 `nil` を返すように変えると、JSON に `null` が現れ、Web 側の `Record<string, number>` 型はパースに失敗します。新しいフィールドを追加するときも同じパターンに従ってください。
- **ディープコピーは既知の参照フィールドのみを対象とします**：`copySnapshot` は 2 つのカウント辞書だけを処理します。将来 `NodeStatus` にスライス／マップのフィールドを追加した場合は同期して拡張しなければならず、そうしないとエンキュー後も呼び出し側に変更されてしまいます。
- **タイムスタンプの単位はミリ秒ではなく秒です**：すべての `*_time` / `timestamp` / `ts` は浮動小数点秒（小数付き可）で、Web 側の `number` + `Date` の秒単位契約と一致します。`UnixMilli` に書き換えないでください。
- **URL 検証は構築期に行われます**：`normalizeBaseURL` は静的な検証だけを行うため、DNS、ポート、TLS、サーバーの健全性は事前に発見できません。これらのエラーは最初の `OnTick`/`HandleRecord` のときにネットワークエラーとして現れます。
- **`newHTTPClient` の分離性は `http.DefaultTransport` の型に依存します**：カスタムの RoundTripper に置き換えられている場合、複数のコンポーネントが同じ Transport を共有し、`CloseIdleConnections` が他のコンポーネントに影響する可能性があります。
- **`toJSONValue` のプローブにはコストがあります**：枯死イベントごとにシードに対して完全な `json.Marshal` の試行が行われるため、大きなオブジェクトでは余分な割り当てが発生します。その代わりに、ペイロードの問題で 1 件のエラー報告が全体として失敗することはありません。
