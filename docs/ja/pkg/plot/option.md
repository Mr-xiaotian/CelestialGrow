# pkg/plot/option.go

> 📅 最終更新日: 2026/11/13

`option.go` はすべてのノードの「関数オプション」設定層を定義します。すべての `Option` は `*plotOptions` を一度変更する小さなクロージャです。`newBasePlot` はまず `defaultOptions()` を取得し、その後ユーザーが渡した `Option` を順に適用するため、`NewSinglePlot` / `NewSplitPlot` / `NewRoutePlot` は同じ設定を共有します。

## 役割

- `numTenders` / `chanSize` / `maxRetries` / `retryDelay` / `retryIf` / `pruneIf` などの調整可能なパラメータを「関数オプション」としてカプセル化します。
- 各ノードのコンストラクタシグネチャを簡潔に保ち（`name, cultivator, opts...`）、将来新しい設定を追加しても API を壊さないようにします。
- `plotOptions` は `basePlot` に直接埋め込まれるため、オプションは 3 種類のノード（`SinglePlot` / `SplitPlot` / `RoutePlot`）にも同様に有効です。

## 主要な型

```go
type Option func(*plotOptions)

type plotOptions struct {
    numTenders int
    chanSize   int
    maxRetries int
    retryDelay func(attempt int) time.Duration
    retryIf    func(error) bool
    pruneIf    func(any) bool
}
```

`Option` は「`*plotOptions` を受け取り、その場で変更し、何も返さない」関数であり、Go でよく見られる省略可能引数のパターンです。

> ログレベルは `plotOptions` に **含まれません**。`Run(seeds, logLevel)` が実行時に `assembly.StartPlotResources` へ渡すため、同じ plot でも実行ごとに異なるログレベルを使用できます。

## 既定値

`defaultOptions()` の実装：

| フィールド | 既定値 | 意味 |
|------|--------|------|
| `numTenders` | `runtime.NumCPU()` | tender（tend goroutine）の並行数 |
| `chanSize` | `runtime.NumCPU()` | `seedChan` のバッファサイズ |
| `maxRetries` | `1` | 最大再試行回数（初回を含まない）。すなわち「既定で 1 回再試行」 |
| `retryDelay` | `func(attempt int) time.Duration { return 0 }` | 再試行前に待機しない |
| `retryIf` | `func(error) bool { return true }` | あらゆるエラーで再試行する |
| `pruneIf` | `nil` | 剪定しない |

## `WithXxx` 関数一覧

下表は `option.go` 内のすべての公開 `WithXxx` 関数を網羅します。引数、既定値、効果はいずれもソースコードを唯一の事実の源とします。

| 関数 | 引数 | 既定値 | 役割 |
|------|------|--------|------|
| `WithTenders(n int) Option` | `n`：tender の goroutine 数 | `runtime.NumCPU()` | `numTenders` を設定し、`sprout` の `sem` セマフォのサイズを制御することで、最大並行育成数を決める |
| `WithChanSize(n int) Option` | `n`：チャネルのバッファサイズ | `runtime.NumCPU()` | `chanSize` を設定し、本ノードの `seedChan` のバッファ割り当てに使用する。下流の `yieldChans` には追加のバッファを割り当てない（下流の `seedChan` を直接再利用する） |
| `WithMaxRetries(n int) Option` | `n`：最大再試行回数（初回を含まない） | `1` | `maxRetries` を設定する。`WithMaxRetries(2)` は最大 3 回の実行（初回 1 回 + 再試行 2 回）を意味する |
| `WithRetryDelay(fn func(attempt int) time.Duration) Option` | `fn`：再試行間隔の戦略 | `func(int) time.Duration { return 0 }` | `retryDelay` を設定する。`attempt` は 1 から増加し、「指数バックオフ」の実装によく使われる |
| `WithRetryIf(fn func(error) bool) Option` | `fn`：エラーフィルタ | `func(error) bool { return true }` | `retryIf` を設定する。`true` を返したエラーだけが次の再試行に参加し、`false` を返すと再試行ループを直ちに終了して `witherSeed` へ進む |
| `WithPruneIf[S any](fn func(S) bool) Option` | `fn`：剪定の述語 | 未設定（`nil`、つまり剪定しない） | `pruneIf` を設定する。述語は `cultivator` の **前** に判定され、`true` を返した seed はそのまま `prune` として終結し、再試行も `cultivator` の呼び出しも行われず、どの下流にも出力されない |

> `WithChanSize` のソースコメントは今も「seedChan/fruitChan」と書かれていますが、現在の骨組みでこの容量を使うのは `seedChan` だけで、`fruitChan` はすでに存在しません。

## 再試行との連携例

次の例は 3 つの Option を組み合わせて「最大 3 回の再試行、attempt に応じた線形バックオフ、恒久的なエラーは再試行しない」を実現します：

```go
p := plot.NewSinglePlot("flaky",
    func(seed int) (int, error) { return doWork(seed) },
    plot.WithMaxRetries(3),
    plot.WithRetryDelay(func(attempt int) time.Duration {
        return time.Duration(attempt) * 100 * time.Millisecond
    }),
    plot.WithRetryIf(func(err error) bool {
        return !errors.Is(err, ErrPermanent)
    }),
)
```

> 再試行ループの具体的な動作は `plot.md` の「再試行ループ `tend`」の節を参照してください。`maxRetries=3` の場合は最大 4 回実行され（初回 1 回 + 再試行 3 回）、最後の失敗時には `SeedReplant` イベントを発行しませんが、それでも `retryDelay(4)` を 1 回実行します。

## 剪定との連携例

以下は `WithPruneIf` を使って「重複した seed はそのまま剪定してスキップし、二度と育成しない」ようにする例です：

```go
var seenSeeds sync.Map // 注意：需 import "sync"

p := plot.NewSinglePlot("dedup",
    func(seed int) (int, error) { return seed * 10, nil },
    plot.WithTenders(2),
    plot.WithPruneIf(func(seed int) bool {
        _, loaded := seenSeeds.LoadOrStore(seed, struct{}{})
        return loaded // 已见即修剪
    }),
)
```

> 剪定のセマンティクスは `plot.md` の「剪定パス `pruneSeed`」の節を参照してください。剪定された seed は `cultivator` の前で打ち切られ、`Skipped` に計上され、`Processed` は保存されます。

## 注意事項

- `WithMaxRetries(n)` の `n` は初回実行を **含みません**。`n=0` は「再試行しない」を意味します。
- `WithRetryDelay` の `attempt` は 1 始まりです。ソースは失敗のたびに `Sleep(retryDelay(attempt))` を実行し、**最後の失敗時も含みます**（このときはもう再試行しません）。そのためバックオフ関数は「空待ちになる可能性のある」位置で余分に 1 回呼ばれます。
- `WithPruneIf` の述語は `cultivator` の前に判定され、しかも **再試行より優先** されます。述語が `true` を返すと、その seed は `cultivator` を呼ばれず再試行ループにも入らず、直接 `pruneSeed` へ進みます。述語の既定値は `nil`（剪定しない）です。
- `WithPruneIf` は内部で型アサーションを使って `any` を `S` に戻します。アサーションに失敗した場合は「剪定しない」として扱われます。ジェネリックパラメータ `S` は渡した述語の引数型から推論されるため、明示する必要はありません。
- ログレベルは `Run` の `logLevel` 引数で制御され、`WithLogLevel` という Option はもう存在しません。
- `Option` は単純なクロージャの重ね合わせであるため、**同じ名前の Option は後から渡したものが先に渡したものを上書きします**。より複雑なマージ戦略が必要な場合は、外側で自分で処理してから 1 回だけ渡してください。
- `numTenders` / `chanSize` の既定値はいずれも `runtime.NumCPU()` で、コンテナ内ではこの値はホストから見える CPU 数を反映します。I/O 集約型の業務では `WithTenders` / `WithChanSize` を組み合わせて明示的に大きくすることを推奨します。
