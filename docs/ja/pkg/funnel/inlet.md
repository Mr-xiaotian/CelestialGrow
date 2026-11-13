# pkg/funnel/inlet.go

> 📅 最終更新日: 2026/11/13

`pkg/funnel/inlet.go` は `Inlet[T]` ジェネリック抽象を定義する——CelestialGrow の非同期消費基盤における**生産側**（書き込み側）である。上流コンポーネントのレコードをバッファ付きの Go channel に送り込み、対となる `Spout[T]` が非同期に消費する。`Inlet` には独立した `context.Context` が組み込まれており、`Close` で能動的にキャンセルできる。さらに各送信にはタイムアウト保護があり、上流が遅い消費者によって無限にブロックされることを防ぐ。

> **命名に関する注意**：`Inlet` は直感的には「入口」のように見えるが、本パッケージにおいては**書き込み側 / 生産側**である。真の「消費ループの入口」は `Spout` である。`Inlet` はレコードをチャネルに「流し込み」、`Spout` はチャネルからレコードを「噴出」して `RecordHandler` に引き渡す。

## 役割

- **タイムアウト**と**コンテキストキャンセル**を備えた安全な `Send` を提供し、無制限のブロッキングを回避する。
- `chan<- T` によって「書き込み専用」の意味を明示し、チャネルの所有権を対となる `Spout` に委ねる。
- `persist` 層のオブザーバーの書き込みチャネルとして機能する：`persist.LogWatcher` / `persist.LifecycleWatcher` はそれぞれ `*funnel.Inlet[T]` を 1 つ保持し、`OnEvent` はイベントをレコードへ変換して送り込むだけである。

## 中核オブジェクト

### `type Inlet[T any]`

```go
type Inlet[T any] struct {
    ch      chan<- T        // 只写通道，由对端 Spout 持有
    timeout time.Duration   // 单次 Send 的最大等待时间
    ctx     context.Context // 内部 context，Close 时被取消
    cancel  context.CancelFunc
}
```

- **ジェネリックパラメータ `T`**：単一レコードの型。たとえば `persist.LogWatcher` は `T = persist.LogRecord` を使用する。
- フィールドは一切エクスポートされず、外部からは `NewInlet` を通じてのみ構築できる。

### 構築：`NewInlet[T any]`

```go
func NewInlet[T any](ch chan<- T, timeout time.Duration) *Inlet[T]
```

- `ch`：`Spout.GetQueue()` から取得した「書き込み専用」ハンドルでなければならない（Go は双方向の `chan T` を自動的に `chan<- T` へ変換する）。
- `timeout`：単一 `Send` の最大待機時間。`0` は「タイムアウトなし」では**ない**——`time.After(0)` のチャネルは即座に読み取り可能になるため、書き込み分岐が準備できていない場合 `Send` は直ちにタイムアウトエラーを返す。正の時間を渡すこと。
- 内部では `context.Background()` をルートとして独立した `ctx` を生成するため、ある `Inlet` のキャンセルが他の `Inlet` に影響することはない。

### 公開メソッド

| メソッド | シグネチャ | 役割 |
|------|------|------|
| `Send` | `func (s *Inlet[T]) Send(record T) error` | レコードを 1 件書き込む。コンテキストのキャンセルまたはタイムアウト時はエラーを返す |
| `Close` | `func (s *Inlet[T]) Close()` | 内部 `ctx` をキャンセルし、ブロック中のすべての `Send` を即座にエラーで返す |

#### `Send(record T) error`

動作は `select` の 3 つの分岐の競合である：

1. `s.ch <- record`：書き込みに成功し、`nil` を返す。
2. `<-s.ctx.Done()`：`s.ctx.Err()`（`context.Canceled`）を返す。
3. `<-time.After(s.timeout)`：`fmt.Errorf("inlet send timeout after %v", s.timeout)` を返す。たとえば `inlet send timeout after 1s`。

> **背圧（バックプレッシャー）戦略**：チャネルが満杯で対となる `Spout` がまだ消費していない場合、`Send` は `timeout` の間ブロックして待機する。タイムアウトは上流の負荷が過大であることを意味するため、呼び出し側でメトリクスを記録するか劣化動作をトリガーすることを推奨する。
>
> **`select` のランダム性に注意**：書き込み分岐と `ctx.Done()` が同時に準備完了した場合（チャネルにまだ空きがあり、かつ `Close` 済み）、Go は準備完了した分岐からランダムに選択するため、`Close` 後の `Send` は**依然として書き込みに成功**し `nil` を返す可能性がある。「もう送信しない」という意味が必要な場合は、呼び出し側が自ら `Send` の呼び出しを停止すべきである。

#### `Close()`

- `s.cancel()` を呼び出して内部 `ctx` をキャンセルするが、チャネルを直接クローズする**ことはない**——チャネルの所有権は `Spout` 側にあり（`Spout.Stop` が `close(ch)` を担当する）。
- 複数回呼び出すことができ、べき等である。

## 並行セマンティクス

- `Send` は「channel への書き込み」というアトミックな操作だけを行い、**複数の goroutine から並行に呼び出せる**。外部でのロックは不要である。`persist` のオブザーバーはまさにこれに依存している：`Hub` は複数の Plot ゴルーチンから同期的に `OnEvent` を呼び出す可能性があり、その結果として同じ `Inlet` に並行して書き込まれる。
- `Close` と `Send` を並行に呼び出してもデータ競合は発生しない（`cancel` は並行安全である）。ただし前述のとおり、`Close` は「以後の書き込みが成功しないこと」を保証しない。
- `Inlet` 自体はハンドラの同期を行わない：並行に書き込まれた複数のレコードは同じキューに入り、最終的に `Spout` の**単一**の消費 goroutine が直列に `HandleRecord` を呼び出す。

## persist / assembly との呼び出し関係

```text
persist.LogWatcher / LifecycleWatcher
   │ OnEvent(event)              // Hub 分发进来的业务事件
   │   └─ 翻译为 LogRecord / LifecycleRecord
   ▼
funnel.Inlet[T].Send(record)
   │
   ▼  chan<- T（来自 Spout.GetQueue()）
funnel.Spout[T] ──HandleRecord──▶ RecordHandler[T]
```

重要な点：

- `persist.LogWatcher` / `persist.LifecycleWatcher` は `*funnel.Inlet[T]` を**保持する**（埋め込まない）。非エクスポートの `newLogWatcher` / `newLifecycleWatcher` 内で `funnel.NewInlet(spout.GetQueue(), time.Second)` によって構築される。
- 2 つのオブザーバーのキュー設定は同じである：`funnel.NewSpout(handler, 100, time.Second)` + `funnel.NewInlet(..., time.Second)`、すなわち**バッファ 100 件**で、`Send` のタイムアウトと `Spout.Stop` のグレースフルシャットダウンのタイムアウトはいずれも **1 秒**である。
- `LogWatcher` は `OnEvent` と `Send` の間にもう 1 層のレベルフィルタを持つ：私有メソッド `log(level, message, at)` は `minLevel` 未満のレコードを破棄し、これらのレコードはチャネルを**占有しない**。
- `Send` のエラーは `OnEvent` を通じてそのまま返される（`LogWatcher.OnEvent` / `LifecycleWatcher.OnEvent` は `Send` の error をそのまま戻り値にする）。ただし `Hub` はオブザーバーのエラーを捕捉して `ErrorHandler` に渡し、常に `nil` を返すため、タイムアウトが業務の実行を中断することはない。
- `Plot` / `Farm` は `Inlet` や `Spout` を**もはや**直接保持しない：保持するのは `scarecrow.Hub` だけであり、オブザーバーは `pkg/assembly` の `StartPlotResources` / `StartFarmResources` が作成して起動し、実行終了時には `Resources.Stop`（内部で `sync.Once`）が先にログを停止し、その後にライフサイクルを停止する。

## 使用例

最小限の `Inlet` + `Spout` の組み合わせで、整数カウンタの非同期永続化フローを示す：

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
)

// FileHandler 演示用：把每条记录落到标准输出。
type FileHandler struct{ count int }

func (h *FileHandler) BeforeStart() error       { return nil }
func (h *FileHandler) HandleRecord(r int) error { h.count++; fmt.Println("got:", r); return nil }
func (h *FileHandler) AfterStop() error         { fmt.Println("total:", h.count); return nil }

func main() {
    handler := &FileHandler{}

    // 1) 构造 Spout（消费端），它持有双向 channel 并暴露只写句柄。
    spout := funnel.NewSpout[int](handler, 8, 2*time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }

    // 2) 构造 Inlet（生产端），绑定到 spout 的写入句柄。
    inlet := funnel.NewInlet[int](spout.GetQueue(), 500*time.Millisecond)

    // 3) 发送一批记录；通道满时 Inlet 会在 500ms 内等待。
    for i := 0; i < 5; i++ {
        if err := inlet.Send(i); err != nil {
            fmt.Println("send err:", err)
        }
    }

    // 4) 先停写入，再停消费：Stop 会 close(ch) 并等待剩余记录处理完。
    inlet.Close()
    if err := spout.Stop(); err != nil {
        fmt.Println("stop err:", err)
    }
}
```

> 順序が重要である：`Spout.Stop()` はチャネルをクローズするため、その時点で `Send` が 1 つも残っていないことを保証しなければならない。`inlet.Close()` は**ブロック中**の `Send` を早く返させるだけで、すでに `select` に入った `Send` が書き込み分岐に到達するのを止めることはできない。

## 注意事項

- `Inlet` 内部の `ctx` は `Close` 時にのみキャンセルされる。`Send` のタイムアウトと `ctx` は**並列**の関係にあり、呼び出し側は 2 種類のエラー（`context.Canceled` と `inlet send timeout after ...`）を同時に扱う必要がある。
- `Send` のエラーは**リトライしない**。リトライ戦略は上位層で実装すべきであり、`Inlet` 内に暗黙のループを持ち込まないこと。
- チャネルの所有権は `Spout` にあり（`Spout.Stop` 内部が `close(ch)` を担当する）、`Inlet` が `close(ch)` してはならない。二重クローズは panic を引き起こし、以後に書き込み分岐へ到達した `Send` も同様に panic する（`send on closed channel`）。
- `Inlet` のゼロ値は使用できない：`ctx` / `cancel` が `nil` のとき `Send` は panic する。必ず `funnel.NewInlet` で構築する必要がある。これを保持する型（`persist.LogWatcher` など）は**ポインタ**フィールドを使うが、コンストラクタ内でしか初期化できないメンバーとみなすべきである。
- `timeout=0` の場合、`time.After(0)` が即座に読み取り可能になるため、`Send` はほぼ常にタイムアウト分岐を通る（`ctx` が既にキャンセルされている場合は 2 つの分岐がランダムになる）——本番環境では必ず妥当な上限を設定すること。
