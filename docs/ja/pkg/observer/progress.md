# pkg/observer/progress.go

> 📅 最終更新日: 2026/09/24

## 役割

`pkg/observer/progress.go` は `Observer` インターフェースの既定のターミナル可視化実装である `ProgressBar` を提供する。サードパーティライブラリ [`github.com/schollz/progressbar/v3`](https://github.com/schollz/progressbar) に基づき、Plot の育成進捗を標準エラー出力上のリアルタイム進捗バーとしてレンダリングし、CLI のケースで大量の種子の処理進捗を観測しやすくする。

## 中核オブジェクト

### `ProgressBar` 型

```go
type ProgressBar struct {
    description string
    bar         *progressbar.ProgressBar
    mu          sync.Mutex
}
```

| フィールド | 役割 |
|------|------|
| `description` | 進捗バーのプレフィックス説明テキスト。`NewProgressBar` で設定された後は読み取り専用 |
| `bar` | 遅延生成される `progressbar.ProgressBar` インスタンス。最初の `OnStart` / `OnProgress` / `OnFinish` で非ゼロの `total` を取得した後に初めて実際に構築される |
| `mu` | `bar` フィールドおよび `bar.Set` / `bar.Finish` の呼び出しを保護するミューテックス。並行安全性を確保する |

`ProgressBar` はエクスポートされた型である（`pkg/api` から `api.NewProgressBar` で取得できる）が、フィールド `description` / `bar` / `mu` はいずれもエクスポートされていないため、`NewProgressBar` を通じてのみ構築できる。これは `observer.Observer` を実装しており、任意の Plot ノードの `AddObserver` に直接渡すことができる。

### `NewProgressBar` 構築

```go
func NewProgressBar(description string) *ProgressBar
```

- `description`：進捗バーのプレフィックステキスト。`OptionSetDescription` で設定されたプレフィックス位置に表示される。
- 戻り値：`description` のみを設定したゼロ状態の `*ProgressBar`。この時点では下位の `bar` はまだ `nil` であり、最初のコールバックが非ゼロの `total` を伴うまで実際には初期化されない。

## 進捗バーの実装原理

`ProgressBar` は `schollz/progressbar/v3` の `*progressbar.ProgressBar` インスタンスを保持する。`OnStart` / `OnProgress` / `OnFinish` の 3 つのメソッドはいずれもまず `p.mu` をロックし、次に `ensureBar` を呼び出してオンデマンドの遅延読み込みをトリガーし、最後にロックを保持した状態で `bar` を操作する：

```go
func (p *ProgressBar) ensureBar(total int) {
    if total == 0 || p.bar != nil {
        return
    }
    p.bar = progressbar.NewOptions64(...)
}
```

- 遅延読み込み：`bar` は `total > 0` かつ未生成の場合にのみ構築されるため、`OnStart(0)` では即座に描画されない。
- ミューテックス：すべての `OnStart` / `OnProgress` / `OnFinish` の入口で `p.mu.Lock(); defer p.mu.Unlock();` を実行し、並行状況下で `bar` が重複生成されたり、並行に `Set` / `Finish` されたりするのを防ぐ。
- 完了コールバック：`OptionOnCompletion` を登録し、進捗バーが満杯になったときに `os.Stderr` へ改行を追加で 1 つ書き込み、後続のログと癒着しないようにする。

## 記述子 / オプション

`ensureBar` は `progressbar.NewOptions64` を通じて以下のオプションを設定する：

| オプション | 役割 |
|------|------|
| `OptionSetDescription(p.description)` | 進捗バーのプレフィックス説明を設定し、`NewProgressBar` で渡された文字列を使用する |
| `OptionSetWriter(os.Stderr)` | 出力先を `os.Stderr` とし、stdout の業務出力と衝突しないようにする |
| `OptionSetWidth(10)` | 進捗バーの文字幅は 10 |
| `OptionShowTotalBytes(true)` | 総量をバイト単位で表示する |
| `OptionThrottle(time.Millisecond)` | レンダリングを 1ms にスロットルし、高並行の `Set` で画面が埋まるのを防ぐ |
| `OptionShowCount()` | 現在のカウントを表示する |
| `OptionShowIts()` | イテレーション回数（毎秒のリフレッシュ率）を表示する |
| `OptionOnCompletion(...)` | 完了時に改行文字を 1 つ出力する |
| `OptionSpinnerType(14)` | 14 番の spinner アニメーションを使用する |
| `OptionFullWidth()` | 進捗バーをターミナル幅いっぱいに広げる（固定 width と併用した場合の挙動はライブラリが決定する） |
| `OptionSetRenderBlankState(true)` | `total == 0` のときに空白のプレースホルダーを描画できるようにする。`ensureBar` は `total == 0` のとき `bar` を生成しないため、このオプションは現在実際には効果がない（`total > 0` になった後の最初のフレームでのみ有効） |

配色は `schollz/progressbar/v3` の既定テーマが提供し、`OptionSetTheme` によるカスタマイズは行っていない。色を変更したい場合は `ensureBar` に `progressbar.OptionSetTheme(...)` の呼び出しを追加する必要がある。

## 3 つのコールバックの具体的な挙動

```go
func (p *ProgressBar) OnStart(total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
}

func (p *ProgressBar) OnProgress(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(completed)
}

func (p *ProgressBar) OnFinish(completed, total int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.ensureBar(total)
    _ = p.bar.Set(total)
    _ = p.bar.Finish()
}
```

- `OnStart`：`bar` が生成済みであることを確認するのみ（`total > 0` の場合に限る）で、進捗は進めない。このメソッドは `total` のみを受け取り、`completed` パラメータはない。
- `OnProgress`：`completed` を `bar` に書き込み、`progressbar/v3` 内部が再描画する。
- `OnFinish`：`bar` を強制的に `total` まで満杯にし（最新の `completed < total` であっても）、次に `Finish()` を呼び出して終了をマークし、`OptionOnCompletion` をトリガーして改行を書き出す。
- `bar.Set` / `bar.Finish` が返すエラーはすべて明示的に `_ =` で無視され、標準ストリームがクローズされたときに io エラーを返すという `progressbar/v3` の一般的なパターンに従い、ノイズログを避ける。

## 使用例

`pkg/plot` と直接組み合わせる：

```go
package main

import (
    "github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
    "github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
)

type Seed struct{ ID int }
type Fruit struct{ ID int }

func cultivate(s Seed) (Fruit, error) {
    return Fruit{ID: s.ID}, nil
}

func main() {
    p := plot.NewPlot[Seed, Fruit]("harvester", cultivate)
    p.AddObserver(observer.NewProgressBar("harvesting"))

    seeds := make([]Seed, 1000)
    for i := range seeds {
        seeds[i] = Seed{ID: i}
    }
    p.Run(seeds) // 内部会调用 StartAsync + Seed + Seal + WaitAsync
}
```

業務コードは通常 `pkg/api` をインポートするだけでよい——`api.NewProgressBar` は `*observer.ProgressBar` を返し、`api.Plot` は `plot.Plot` の型エイリアスであるため、`AddObserver` も同様に使用できる：

```go
import "github.com/Mr-xiaotian/CelestialGrow/pkg/api"

p := api.NewPlot[Seed, Fruit]("harvester", cultivate)
p.AddObserver(api.NewProgressBar("harvesting"))
```

実行すると `os.Stderr` に以下のようなものが現れる：

```
harvesting |█████████-| 1,000/1,000 [100%] 2.5s
```

## 注意事項

- **出力媒体は `os.Stderr` に固定されている**：`OptionSetWriter` で明示的に設定されているため、呼び出し側が stderr を `/dev/null` にリダイレクトした場合、進捗バーは見えなくなるが Plot の正常な動作には影響しない。
- **幅は 2 つのオプションの影響を同時に受ける**：`ensureBar` は `OptionSetWidth(10)` と `OptionFullWidth()` を同時に渡すため、実際の描画幅はライブラリがターミナルの列数と組み合わせて決定する。狭いターミナルで安定した幅を得たい場合は、`OptionFullWidth()` を削除して固定の `OptionSetWidth` を保持すべきである。
- **再入安全**：`bar` は最大 1 回だけ代入される（`ensureBar` は `p.bar != nil` のときそのまま返る）。並行に発火した `OnStart` / `OnProgress` / `OnFinish` は `mu` により直列化され、race は発生しない。
- **エラーの無視**：`bar.Set` / `bar.Finish` の `error` は明示的に破棄され、ターミナル UI のケースにおける「描画の失敗が主フローに影響すべきでない」という慣例に合致する。
- **再利用不可**：`ProgressBar` は単一の Plot ライフサイクルに紐付けられ、`Reset` / `ResetTotal` メソッドはない。別のバッチタスクで再利用したい場合は `NewProgressBar` し直すこと。
