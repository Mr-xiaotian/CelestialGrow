# pkg/plot/constant.go

> 📅 最終更新日: 2026/09/24

`constant.go` は `plot` パッケージ内の非公開定数の集合です。現在はセンチネル文字列を 1 つだけ定義しており、`runtime.Payload.Source` フィールドで「外部の呼び出し元」と「ある名前付きの上流 plot」を区別するために使用します。

## 役割

- `Payload.Source` に安定したセンチネル値を与え、実装中にリテラル `"__input__"` が散らばるのを避けます。
- `basePlot.Seal` と `basePlot.markSealed` と組み合わせて「**外部からの `Seal()` は強制終了**」というセマンティクスを実現します。

## 公開シンボル

このファイルは **非公開定数を 1 つだけ** 含みます：

```go
const sourceInput = "__input__"
```

| 定数 | 型 | 値 | 用途 |
|------|------|----|------|
| `sourceInput` | `string` | `"__input__"` | `runtime.Payload.Source` のセンチネル値として、「この seal は外部の呼び出し元からのもので、特定の上流 plot からではない」ことを表す |

> 非公開定数であるため、外部パッケージから **直接参照できません**。「ある Payload が外部から注入されたかどうか」を判定したい場合は、この値を取り出すのではなく、`Seal` / `Seed` の動作から推論してください。

## 使用箇所

`sourceInput` は `pkg/plot` 内で合計 2 箇所で使用されており、いずれも `pkg/plot/plot_base.go` にあります：

1. `basePlot.Seal()`：

   ```go
   func (p *basePlot[S, F, Y]) Seal() {
       sealID := p.eventClient.Emit("seal", []int{})
       p.seedChan <- runtime.Payload[S]{Signal: runtime.SignalSeal, Source: sourceInput, EventID: sealID}
   }
   ```

   この seal が外部からの `Seal()` 呼び出しによるものであることを示します。

2. `basePlot.markSealed`：

   ```go
   if source == sourceInput {
       sealedFrom[sourceInput] = sealID
       return true
   }
   ```

   `markSealed` は `Source == sourceInput` を見た時点で **直接 `true` を返し**（入力はすでに閉じている）、**他の上流の seal を待たなくなります**。これが外部 `Seal()` の「強制終了」セマンティクスです。

一方、通常の経路での seal の伝播は `sprout` 自身の終了処理に由来します：

```go
sealPayload := runtime.Payload[Y]{Signal: runtime.SignalSeal, Source: p.name, EventID: sealID}
```

`Source` は現在の plot の `name` であることに注意してください（`sourceInput` ではありません）。これにより下流が由来を正しく識別できます。

## 注意事項

- 外部パッケージで `"__input__"` を再定義したりハードコードしたりしないでください。この文字列は `pkg/plot` の内部実装の詳細であり、将来変更される可能性があります。
- `Source == sourceInput` の seal を生成するのは `Seal()` だけです。上流 plot が終了時に発行する seal の `Source` は一律で自分自身の `name` です。
- Farm モードで、ある下流 plot が（他の上流を待たずに）早く終了しているように見える場合、高い確率で上流の 1 つが自身の `Seal()` を呼び出してこの強制終了の経路が発動しています。
