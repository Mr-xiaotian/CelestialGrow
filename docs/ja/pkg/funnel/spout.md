# pkg/funnel/spout.go

> 📅 最終更新日: 2026/09/24

`pkg/funnel/spout.go` は `Spout[T]` ジェネリック抽象を定義する——CelestialGrow の非同期消費基盤における**消費側**（読み取り側）である。バッファ付きの Go channel を保持し、バックグラウンド goroutine で継続的にレコードを読み取って `RecordHandler[T]` に引き渡す。同時に `BeforeStart` / `HandleRecord` / `AfterStop` という 3 段階のインターフェースによってリソースのライフサイクルを明確にする。

> **命名に関する注意**：`Spout` は直感的には「出口」のように見えるが、本パッケージにおいては**消費側**である。`Spout` は内部のチャネルからレコードを「噴出し」続けて handler に渡し、対となる `Inlet` こそがレコードを「流し込む」生産側である。

## 役割

- バックグラウンド goroutine でバッファ付き channel から継続的にレコードを読み取る。
- 「業務処理」を `RecordHandler[T]` インターフェースとして注入し、異なる永続化バックエンド（ファイル / SQLite）間での再利用を可能にする。
- 2 段階のシャットダウンを提供する。まず `close(ch)` でグレースフル終了をトリガーし、タイムアウトしても終了しない場合は `cancel()` で強制終了させ、呼び出し側が無限に待つことを防ぐ。

## 中核オブジェクト

### `type RecordHandler[T any]`（インターフェース）

```go
type RecordHandler[T any] interface {
    BeforeStart() error
    HandleRecord(record T) error
    AfterStop() error
}
```

| メソッド | 呼び出されるタイミング | 典型的な用途 |
|------|-----------|----------|
| `BeforeStart() error` | `Spout.Start` 内部、消費 goroutine の起動**前** | ファイルのオープン、データベース接続の確立、バッファの初期化 |
| `HandleRecord(record T) error` | レコードを 1 件読み取るたび | 単一レコードのシリアライズ / 書き込み |
| `AfterStop() error` | `Spout.Stop` の終了段階。**タイムアウトの有無にかかわらず**呼び出される | ファイルのクローズ、`fsync`、接続の解放 |

3 つのメソッドが返す `error` の扱いは異なる。`BeforeStart` のエラーは `Start` がそのまま返す。`HandleRecord` のエラーは `spout()` によって**黙って破棄**される。`AfterStop` のエラーは `Stop()` によって**黙って破棄**される（`Stop` はシャットダウンタイムアウトのエラーのみを返す）。したがって、handler 内で重大な失敗を戻り値だけに委ねてはならない。

### `type Spout[T any]`

```go
type Spout[T any] struct {
    ch      chan T               // 内部缓冲通道，对外通过 GetQueue 暴露只写句柄
    wg      sync.WaitGroup       // 跟踪消费 goroutine
    timeout time.Duration        // Stop 时优雅关闭的最大等待时间
    ctx     context.Context      // 内部 ctx，被 Stop 在超时时 cancel
    cancel  context.CancelFunc
    handler RecordHandler[T]     // 注入的处理器
}
```

- **ジェネリックパラメータ `T`**：単一レコードの型。たとえば `funnel.NewSpout[persist.LogRecord](...)`。
- フィールドはすべてエクスポートされておらず、構築とライフサイクル管理は公開メソッドを通じてのみ行える。

### 構築：`NewSpout[T any]`

```go
func NewSpout[T any](handler RecordHandler[T], bufferSize int, timeout time.Duration) *Spout[T]
```

- `handler`：`RecordHandler[T]` を実装している必要があり、実装クラスのすべてのメソッドは直列に呼び出される。
- `bufferSize`：内部の `make(chan T, bufferSize)` の容量。`0` はバッファなしチャネルを生成する（合法だが、`Send` が即座に背圧に直面する）。**負数**を渡すと `make` が panic する（現在の実装では検証を行っていない）。
- `timeout`：`Stop` 段階のグレースフルシャットダウンの最大待機時間。`0` はほぼ即座に「強制キャンセル」分岐を通ることを意味する（`time.After(0)` が即座に読み取り可能になる）。

### 公開メソッド

| メソッド | シグネチャ | 役割 |
|------|------|------|
| `GetQueue` | `func (s *Spout[T]) GetQueue() chan<- T` | 書き込み専用ハンドルを返し、`Inlet` がバインドする |
| `Start` | `func (s *Spout[T]) Start() error` | `handler.BeforeStart` を呼び、消費 goroutine を起動する |
| `Stop` | `func (s *Spout[T]) Stop() error` | まず `close(ch)` でグレースフル終了。タイムアウトなら `cancel()` で強制終了し、最終的に `handler.AfterStop()` を呼ぶ |
| `Handler` | `func (s *Spout[T]) Handler() RecordHandler[T]` | 現在注入されている handler を返す。デバッグやテストのアサーションに便利 |

> `Spout` は同期的な「1 件取得」メソッドを提供しない。レコードはバックグラウンド goroutine によってのみ自動的にチャネルから取得され、外部には「起動・停止 + キュー入口」のみを公開する。

#### `GetQueue() chan<- T`

- 呼び出すたびに同一の `chan T` を返す（コンパイラが双方向チャネルから書き込み専用型を派生させる）。
- 呼び出し元は通常 `funnel.NewInlet[T](spout.GetQueue(), timeout)` または `persist.NewLogInlet(spout.GetQueue(), ...)` である。
- このチャネルを他の場所で書き込んだりクローズしたりしては**ならない**——所有権は `Spout` にある。

#### `Start() error`

1. `s.handler.BeforeStart()` を呼び出す。非 `nil` のエラーが返された場合は goroutine を起動**せず**、そのエラーをそのまま返す。
2. `s.wg.Add(1)` の後に `go s.spout()` する。
3. 成功時は即座に返る（非ブロッキング）。エラーが返る前に `BeforeStart` が既にファイルなどのリソースを開いている可能性があるため、呼び出し側が補償的なクリーンアップが必要かどうかを自ら判断すべきである。

#### `Stop() error`

```text
Stop()
  ├─ close(s.ch)                      // 触发 spout() 在 <-s.ch 处返回
  ├─ 等待 wg.Wait() done 通道，最多 timeout
  │     ├─ 正常 done        → err = nil
  │     └─ timeout 触发     → s.cancel() 强制 spout 走 <-s.ctx.Done()
  │                            err = errors.New("shutdown timeout")
  └─ 无论是否超时，都执行 s.handler.AfterStop()          // 返回值被丢弃
```

重要な点：

- チャネルを `close(s.ch)` してはじめて `<-s.ch` の `ok=false` 分岐が成立し、`spout()` がグレースフルに返ることができる。
- 内部 `ctx` は**タイムアウト**時にのみ `cancel()` されるため、**正常系では `ctx` がトリガーされることは決してない**。これは処理中の handler を誤って殺さないための設計上の選択である。
- `AfterStop` は**すべての**終了経路で呼び出され、リソースのリークを防ぐ（`defer` の意味論）。
- 返される `error` はタイムアウトの状況でのみ非 `nil` となり、呼び出し側はこれによってリトライやアラートが必要かを判定できる。

#### `Handler() RecordHandler[T]`

- 構築時に注入された handler（インターフェース値のコピー）を返す。実行時に handler の状態をレポートしたり、単体テストのアサーションなどに使用できる。
- 読み取りのみ可能で、handler の差し替えには**使用できない**。`handler` フィールドはエクスポートされておらず setter もないため、このコピーを変更しても `Spout` 内部が保持するインスタンスには影響しない。

### 内部メソッド `spout()`（非エクスポート）

消費ループ：

```go
for {
    select {
    case record, ok := <-s.ch:
        if !ok { return }                    // 通道关闭：优雅退出
        s.handler.HandleRecord(record)       // 返回值被丢弃
    case <-s.ctx.Done():
        return                              // Stop 超时：强制退出
    }
}
```

- `Start` が起動する goroutine の入口である。`wg.Done()` は `defer` 内で呼ばれ、カウントが確実にゼロになるようにする。
- 直列処理：単一 goroutine が順番に `HandleRecord` を呼び出すため、handler 内で自前のロックを取る必要はない。
- 2 つの終了経路：`ok=false`（チャネルが `Stop` によってクローズされた）または `ctx.Done()`（`Stop` のタイムアウトによる強制終了）。

## Plot / persist との接続点

- `persist.LogRecordHandler` / `persist.LifecycleRecordHandler` はどちらも `RecordHandler[T]` を実装している：
  - `BeforeStart`：`logs/` ディレクトリを作成し、日付ごとに命名されたログファイルを開く / `lifecycles/<date>/` ディレクトリを作成し、SQLite を開く。
  - `HandleRecord`：1 行のテキストにフォーマットしてファイルに追記する / `Kind`（`seed` / `ripen` / `wither`）に従ってイベントを挿入し、状態のスナップショットを更新する。
  - `AfterStop`：ログファイルをクローズする / SQLite 接続をクローズする。
- `Farm.NewFarm` は構築時に 2 つの `Spout` を同時に生成する。`logSpout`（`persist.LogRecord` を処理）と `lifecycleSpout`（`persist.LifecycleRecord` を処理）であり、それぞれの `GetQueue()` ハンドルを `LogInlet` と `LifecycleInlet` に与える。
- `Plot` は Farm モードでは `Spout` を直接保持しない——チャネルは `Farm.Run` が `BindInlet` を通じて注入する。standalone モードでは `Plot.Run` 自身が 2 つの `Spout` を生成し、`StartSpouts` / `StopSpouts` で起動・停止を制御する。

```text
persist.LogInlet / LifecycleInlet ──内嵌──▶ funnel.Inlet[T] ──Send(T)──┐
                                                                      ▼
                                              chan T（Spout 内部缓冲通道）
                                                                      │
                          ┌──GetQueue() 把同一个 chan T 暴露给 Inlet───┘
                          ▼
funnel.Spout[T]（后台消费 goroutine）──HandleRecord(T)──▶ persist.LogRecordHandler / LifecycleRecordHandler
```

## Inlet との協調（inlet ← spout）

```go
// 1. Spout 持有通道
spout := funnel.NewSpout[persist.LogRecord](&persist.LogRecordHandler{}, 100, time.Second)

// 2. 启动消费循环
spout.Start()
defer spout.Stop()

// 3. Inlet 绑定到 Spout 暴露的只写句柄
inlet := funnel.NewInlet[persist.LogRecord](spout.GetQueue(), 500*time.Millisecond)
defer inlet.Close()

// 4. 上游只需调 inlet.Send(...)；通道满了会等 500ms，超时返回错误
```

タイミングの保証：

1. `Spout.Start` の後にのみ `GetQueue` して `Inlet` を構築できる——そうしないと送信されたデータが失われる可能性がある（消費者がいない）。
2. `defer inlet.Close()` + `defer spout.Stop()` の順序により「先に書き込みを停止し、次に読み取りを停止する」ことが保証され、書き込み側が `Stop` の後にまだデータを送ろうとすることを防ぐ。
3. `Spout.Stop` 内部で `close(ch)` が行われるため、**`Send` を呼び出す goroutine が他に存在しないことを必ず確認してから** `Stop` を呼び出す必要がある。クローズ済みのチャネルへの並行書き込みは panic する（`send on closed channel`）。`inlet.Close()` は `ctx` をキャンセルするだけであり、すでに `select` に入った `Send` が書き込み分岐に到達して panic することを**防げない**ことに注意——`Close` が保証するのは「ブロック中の `Send` が素早く `context.Canceled` を返すこと」だけである。**推奨順序**：上流が `Send` の呼び出しを停止 → `inlet.Close()` → `spout.Stop()`。

## 使用例

最小限の `Spout` + `Inlet` の組み合わせ（`inlet.md` と対になる）：

```go
package main

import (
    "fmt"
    "sync"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
)

type PrintHandler struct {
    mu  sync.Mutex
    cnt int
}

func (p *PrintHandler) BeforeStart() error       { fmt.Println("spout start"); return nil }
func (p *PrintHandler) HandleRecord(r int) error { p.mu.Lock(); p.cnt++; p.mu.Unlock(); return nil }
func (p *PrintHandler) AfterStop() error         { fmt.Println("spout stop, total:", p.cnt); return nil }

func main() {
    handler := &PrintHandler{}

    spout := funnel.NewSpout[int](handler, 16, time.Second)
    if err := spout.Start(); err != nil {
        panic(err)
    }

    inlet := funnel.NewInlet[int](spout.GetQueue(), 200*time.Millisecond)
    for i := 0; i < 100; i++ {
        _ = inlet.Send(i)
    }
    inlet.Close() // 先停写入
    if err := spout.Stop(); err != nil {
        fmt.Println("shutdown timeout:", err)
    }
}
```

## 注意事項

- `Spout` は**単一 goroutine による直列**消費である。`HandleRecord` で時間のかかる操作（同期ネットワークリクエストなど）を行うべきではない。そうでなければパイプライン全体のボトルネックとなる。並列処理が必要な場合は `HandleRecord` の内部でさらにディスパッチすること。
- `bufferSize` と `timeout` は重要なチューニングポイントである：
  - `bufferSize` が大きいほど吸収できるバーストトラフィックが増えるが、メモリ使用量も増える。
  - `timeout` が大きいほどシャットダウンは「穏やか」になるが、Farm / CLI の終了時に長く待たされることになる。
- `RecordHandler.HandleRecord` のエラーは現在無視されるため、重大なエラー状態をその戻り値に書き込んでは**ならない**。失敗を可観測にしたい場合は handler 内部で自ら計測点を設けること（`persist.LogInlet` / `LifecycleInlet` の設計を参照）。
- `Stop` 後の `Spout` インスタンスは再利用できない。`ch` は既に `close` されているため、再度 `Start` しても `ok=false` で即座に終了する goroutine が起動するだけであり、再度 `Stop` するとクローズ済みのチャネルを二重にクローズして panic する（`close of closed channel`）。
- `Handler()` が返す参照は読み取り専用である。その内部状態を外部から変更することは許容される（例：書き込み済み件数の照会）が、handler 全体を差し替えても効果はない。
