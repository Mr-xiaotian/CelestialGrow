# pkg/metronome/event.go

> 📅 最終更新日: 2026/11/26

`pkg/metronome/event.go` は値オブジェクト `TickEvent` を 1 つだけ定義します——節拍システムが各回の発火時にすべての `Ticker` へ渡す唯一のペイロードです。これにより「何拍目か」と「いつ実際に発火したか」が 1 つにまとめられ、受け手は間引き／サンプリングの判断にも、時間に敏感な計測にも使えます。

## 役割

- `Ticker.OnTick(event TickEvent)` の唯一の引数として、`Clock → Hub → 子 Hub → リーフ Ticker` の配信チェーン全体を貫きます。
- **単調増加のシーケンス番号** `Seq` を保持し、`Periodic` のフィルタ（`(Seq-1)%period == 0`）や業務側の「N 拍ごとに 1 回実行する」ロジックに使われます。
- **実際の発火時刻** `FiredAt` を保持し、遅延の計算、サンプリング周期、ログのタイムスタンプ整合が必要な ticker に使われます。

## 主要オブジェクト

### `type TickEvent struct`

```go
type TickEvent struct {
    Seq     int       // 节拍序号，从 1 开始
    FiredAt time.Time // 实际触发时间
}
```

| フィールド | 型 | 意味 |
|------|------|------|
| `Seq` | `int` | 節拍のシーケンス番号で、**1 から始まり**単調増加します。これは「クロックが何回目に実際に発火したか」の計数であり、「何回目に特定の ticker へ配信されたか」の計数ではありません。 |
| `FiredAt` | `time.Time` | クロックが `time.Now()` を読んで得た実際の発火時刻で、**単調クロック成分**を持ちます。 |

この構造体にはメソッドがなく、ロックも保持しないため、自由にコピーでき、goroutine 間で受け渡せます。

## 重要な詳細

### `Seq` の意味の境界

- **起点は 1**：`Clock.run` では `seq := 0` で、実際にグリッドの発火点に到達するたびにまず `seq++` するため、最初のイベントは `Seq: 1` になります。テスト `TestClockTicksRoot` は `[]int{1, 2, 3}` を受け取ることを直接アサートしています。
- **生成されるのは Clock 層のみ**：`Hub.OnTick` は上流イベントの `Seq` をそのまま透過させ、番号を振り直しません。したがってネストした Hub ツリーでは、すべての ticker が見る `Seq` は同一のクロック系列に属します。
- **周期フィルタで除外された拍も番号を消費する**：ある `TickPeriod() = 2` の ticker は `Seq = 1, 3, 5, ...` しか受け取りませんが、`2, 4` といった番号は実際にクロックによって消費されています。これは `TestClockRespectsRootTickPeriod` / `TestHubFiltersByTickerPeriod` の期待と一致します。
- **再起動でゼロに戻る**：`Clock` が `Stop` された後に再び `Start` すると、`run` は `seq = 0` からやり直し、新しい系列の最初のイベントも `Seq: 1` になります。再起動をまたぐグローバル一意 ID として扱ってはいけません。
- **型は `int`**：極端に長時間の実行（1 ミリ秒 1 拍で約 29 万年）でのみオーバーフローに達するため実用上は心配不要ですが、`Seq` を「グローバル一意のシーケンス番号」として永続化する場合は注意が必要です。

### `FiredAt` と単調クロック

`FiredAt` は `time.Now()` に由来し、Go ランタイムが単調クロックの読み値を付与します。`Clock` の内部では `time.Until` / `Before` / `Sub` でスケジューリング演算を行い、これらの操作は単調成分を優先するため：

- システムの壁時計（NTP 時刻補正、手動の時刻変更、タイムゾーン変更）によって節拍が加速・逆行・停止することは**ありません**。
- ただし `FiredAt` を文字列に整形したときに見えるのは壁時計の時刻であり、内部のスケジューリングに使う単調読み値とはずれている可能性があります。

`FiredAt` は**実際の発火時刻**であり計画時刻ではありません。前の拍の処理に時間がかかった場合、現在のイベントも実際の `time.Now()` を記録するため、グリッド点より明らかに遅れます（詳細は [`clock.md`](clock.md) のアンカーと拍のスキップの節を参照）。

## 使用例

### ticker 内でシーケンス番号による間引き

```go
package main

import (
    "fmt"
    "time"

    "github.com/Mr-xiaotian/CelestialGrow/pkg/metronome"
)

// EveryFiveTicks 只在第 1、6、11… 拍执行。
type EveryFiveTicks struct{}

func (EveryFiveTicks) OnTick(event metronome.TickEvent) error {
    if event.Seq%5 != 1 {
        return nil
    }
    fmt.Printf("第 %d 拍，于 %s\n", event.Seq, event.FiredAt.Format(time.RFC3339))
    return nil
}
```

> より推奨される方法は `metronome.Periodic` を実装し（`TickPeriod() = 5` を返す）、`Hub` が配信前に一括でフィルタすることです。各 ticker が剰余計算を重複して書かずに済みます。両者の意味は等価です（`Periodic` は `(Seq-1)%period == 0` を使用）。

### `FiredAt` でスケジューリング遅延を観測する

```go
type LagProbe struct{ last time.Time }

func (p *LagProbe) OnTick(event metronome.TickEvent) error {
    if !p.last.IsZero() {
        fmt.Println("相邻两拍实际间隔:", event.FiredAt.Sub(p.last))
    }
    p.last = event.FiredAt
    return nil
}
```

`Clock` は「拍をスキップし、追いかけない」戦略を採るため、時間のかかる処理の後ではこの間隔が公称周期より大きくなります。これは想定どおりの動作です。

## 注意事項

- **イベントを書き換えない**：`TickEvent` は値渡しなので、ticker による引数の変更は他の受け手に影響しません。ロックも不要です。
- **`Seq` を業務の主キーにしない**：`Clock` の再起動でゼロに戻り、スキップされた拍は番号が補充されません。
- **`FiredAt` は想定より遅れることがある**：実際の発火時刻を記録するため、スケジューリングが遅れたときに計画時刻を偽装しません。
- **`FiredAt` の零値に意味はない**：`TickEvent{Seq: 1}` を手動で構築した場合（単体テストで `Hub.OnTick` を直接呼ぶなど）、`FiredAt` は零値になりますが、`Seq` のみに依存するロジックは影響を受けません。
