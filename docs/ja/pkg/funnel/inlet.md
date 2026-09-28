# pkg/funnel/inlet.go

> 📅 最終更新日: 2026/09/24

`pkg/funnel/inlet.go` は `Inlet[T]` ジェネリック抽象を定義する——CelestialGrow の非同期消費基盤における**生産側**（書き込み側）である。上流コンポーネント（`Plot`、`Farm`）が産出したレコードをバッファ付きの Go channel に送り込み、対となる `Spout[T]` が非同期に消費する。`Inlet` には独立した `context.Context` が組み込まれており、`Close` で能動的にキャンセルでき、また各送信にはタイムアウト保護があるため、上流が遅い消費者によって無限にブロックされることを防ぐ。

> **命名に関する注意**：`Inlet` は直感的には「入口」のように見えるが、本パッケージにおいては**書き込み側 / 生産側**である。真の「消費ループの入口」は `Spout` である。`Inlet` はレコードをチャネルに「流し込み」、`Spout` はチャネルからレコードを「噴出」して `RecordHandler` に引き渡す。

## 役割

- **タイムアウト**と**コンテキストキャンセル**を備えた安全な `Send` を提供し、無制限のブロッキングを回避する。
- `chan<- T` によって「書き込み専用」の意味を明示し、チャネルの所有権を対となる `Spout` に委ねる。
- 上位層（`persist.LogInlet`、`persist.LifecycleInlet`）に埋め込み可能な「生産者基底クラス」を提供する。

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

- **ジェネリックパラメータ `T`**：単一レコードの型。たとえば `persist.LogInlet` は `T = persist.LogRecord` を使用する。
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
3. `<-time.After(s.timeout)`：`fmt.Errorf("inlet send timeout after %v", s.timeout)` を返す。

> **背圧（バックプレッシャー）戦略**：チャネルが満杯で対となる `Spout` がまだ消費していない場合、`Send` は `timeout` の間ブロックして待機する。タイムアウトは上流の負荷が過大であることを意味するため、呼び出し側でメトリクスを記録するか劣化動作をトリガーすることを推奨する。`Close` はブロック中のすべての `Send` を直ちに 2 番目の分岐で `context.Canceled` として返させる。
>
> **`select` のランダム性に注意**：書き込み分岐と `ctx.Done()` が同時に準備完了した場合（チャネルにまだ空きがあり、かつ `Close` 済み）、Go は準備完了した分岐からランダムに選択するため、`Close` 後の `Send` は**依然として書き込みに成功**し `nil` を返す可能性がある。「もう送信しない」という意味が必要な場合は、呼び出し側が自ら `Send` の呼び出しを停止すべきである。

#### `Close()`

- `s.cancel()` を呼び出して内部 `ctx` をキャンセルするが、チャネルを直接クローズする**ことはない**——チャネルの所有権は `Spout` 側にあり（`Spout.Stop` が `close(ch)` を担当する）。
- 複数回呼び出すことができ、べき等である。

## Plot / persist との接続点

```text
Plot 内部调用
   │ logInlet.SeedRipen(...)        // 业务侧
   │ lifecycleInlet.SeedRipen(...)
   ▼
persist.LogInlet  ──内嵌──▶  funnel.Inlet[LogRecord]
persist.LifecycleInlet ──内嵌──▶  funnel.Inlet[LifecycleRecord]
                            │
                            ▼  Send(record)
                       chan<- LogRecord  ◀── 来自 Spout.GetQueue()
                            │
                            ▼
funnel.Spout[LogRecord]  ──回调──▶  persist.LogRecordHandler.HandleRecord
```

重要な点：

- `persist.LogInlet` は `funnel.Inlet[LogRecord]` を**埋め込み**、外部には `FarmStart` / `FarmEnd` / `PlotStart` / `PlotEnd` / `SeedInput` / `SeedRipen` / `SeedWither` / `SeedReplant` などの意味付けされたメソッドを公開する。その私有メソッド `log` の内部では依然として `Inlet.Send` を通る。`minLevel` 未満のログは `log` の入口で破棄され、チャネルを**占有しない**。
- `persist.LifecycleInlet` も同様に `Inlet[LifecycleRecord]` を埋め込み、その上に `SeedInput` / `SeedRipen` / `SeedWither` の 3 つのライフサイクル計測点を重ねる。
- `Plot.BindInlet` は `Farm.Run` から一括して呼び出され、`f.logSpout.GetQueue()` と `f.lifecycleSpout.GetQueue()` を渡すことで、「生産側」と「消費側」を同一のチャネルにバインドする。
- standalone モードでは、`Plot` 自身が `Spout` を保持し、`StartSpouts` / `StopSpouts` で起動・停止を制御する。チャネルも同様に `Spout.GetQueue()` から取得する。

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

func (h *FileHandler) BeforeStart() error         { return nil }
func (h *FileHandler) HandleRecord(r int) error   { h.count++; fmt.Println("got:", r); return nil }
func (h *FileHandler) AfterStop() error           { fmt.Println("total:", h.count); return nil }

func main() {
    handler := &FileHandler{}

    // 1) 构造 Spout（消费端），它持有双向 channel 并暴露只写句柄。
    spout := funnel.NewSpout[int](handler, 8, 2*time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }
    defer spout.Stop()

    // 2) 构造 Inlet（生产端），绑定到 spout 的写入句柄。
    inlet := funnel.NewInlet[int](spout.GetQueue(), 500*time.Millisecond)
    defer inlet.Close()

    // 3) 发送一批记录；通道满时 Inlet 会在 500ms 内等待。
    for i := 0; i < 5; i++ {
        if err := inlet.Send(i); err != nil {
            fmt.Println("send err:", err)
        }
    }

    time.Sleep(100 * time.Millisecond) // 给 spout 时间消费
}
```

## 注意事項

- `Inlet` 内部の `ctx` は `Close` 時にのみキャンセルされる。`Send` のタイムアウトと `ctx` は**並列**の関係にあり、呼び出し側は 2 種類のエラーを同時に扱う必要がある。
- `Send` のエラーは**リトライしない**。リトライ戦略は上位層（例：`Plot` の `maxRetries`）で実装すべきであり、`Inlet` 内に暗黙のループを持ち込まないこと。
- チャネルの所有権は `Spout` にあり（`Spout.Stop` 内部が `close(ch)` を担当する）、`Inlet` が `close(ch)` してはならない。二重クローズは panic を引き起こし、以後に書き込み分岐へ到達した `Send` も同様に panic する（`send on closed channel`）。
- `Inlet` を埋め込むサブタイプ（例：`persist.LogInlet`）が埋め込むのは**値**（`funnel.Inlet[LogRecord]`）であり、ゼロ値は使用できない。コンストラクタは必ず `funnel.NewInlet` を呼び出して初期化を完了する必要があり、そうしなければ `Send` / `Close` は `ctx` / `cancel` が `nil` であるために panic する。
- `timeout=0` の場合、`time.After(0)` が即座に読み取り可能になるため、`Send` はほぼ常にタイムアウト分岐を通る（`ctx` が既にキャンセルされている場合は 2 つの分岐がランダムになる）——本番環境では必ず妥当な上限を設定すること。
