# pkg/plot/helper.go

> 📅 最終更新日: 2026/11/13

`helper.go` は `plot` パッケージ内の非公開ユーティリティ関数の集合です。現在は文字列を切り詰める関数 `trunc` を 1 つだけ提供しており、イベントを送信する前に seed / fruit / yield の文字列表現を短い形式に圧縮し、イベントとそれに伴って生成されるログ行が長くなりすぎるのを避けるために使われます。

## 役割

- 「先頭と末尾をそれぞれ 1/3 ずつ残し、中間を `...` で置き換える」文字列の切り詰め機能を提供します。
- `basePlot` と 3 種類のノードが `SeedInputEvent` / `SeedRipenEvent` / `SeedWitherEvent` / `SeedPruneEvent` / `SeedReplantEvent` の `SeedRepr` / `FruitRepr` フィールドを構築する際に、`fmt.Sprintf("%+v", value)` の結果を短く圧縮するために使用します。

## 公開シンボル

このファイルは **非公開関数を 1 つだけ** 含みます：

### `trunc(s string, maxLen int) string`

文字列 `s` を最大 `maxLen` 個の rune に切り詰めます：

- `len([]rune(s)) <= maxLen` の場合はそのまま返します。
- それ以外の場合は先頭 `segmentLen = max(1, maxLen/3)` 個の rune と末尾 `segmentLen` 個の rune を取り、中間を `"..."` で連結して返します。

```go
func trunc(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	segmentLen := max(1, maxLen/3)
	headStr := string(runes[:segmentLen])
	tailStr := string(runes[len(runes)-segmentLen:])
	return headStr + "..." + tailStr
}
```

| 引数 | 意味 |
|------|------|
| `s` | 元の文字列 |
| `maxLen` | 切り詰めの上限（rune 単位で数え、バイトではありません） |

| 戻り値 | 意味 |
|------|------|
| `string` | 上限を超える場合は `<先頭 1/3>...<末尾 1/3>` の形になります。実際の長さは後述の「動作の詳細」を参照してください |

## 動作の詳細

- **rune 単位で数える**：`[]rune(s)` への変換を使用し、中国語などのマルチバイト文字が途中で切れるのを避けます。
- **`max(1, maxLen/3)` によるフォールバック**：`maxLen < 3` の場合でも `segmentLen >= 1` を保証し、「切り詰め後に空文字列になる」事態を避けます。
- **元の文字列を変更しない**：`s` は値で渡され、`runes` を読み取るだけです。
- **切り詰め後の長さ**：結果は `2*segmentLen + 3` 個の rune です（`segmentLen = max(1, maxLen/3)`）。パッケージ内で使用される 25 / 50 では結果は `maxLen` よりはるかに小さくなります。`maxLen` が非常に小さい場合（3、4、6 など）にのみ上限を超える可能性があるため、厳密な長さ上限として扱わないでください。

## `plot` パッケージ内での使用箇所

現在の `trunc` の呼び出し箇所は `basePlot` の骨組みと 3 種類のノードの成功 / 失敗パスに分布しています：

| 位置 | 呼び出し | 書き込まれるイベントフィールド |
|------|------|----------------|
| `basePlot.Seed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedInputEvent.SeedRepr` |
| `basePlot.witherSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedWitherEvent.SeedRepr` |
| `basePlot.pruneSeed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | `SeedPruneEvent.SeedRepr` |
| `basePlot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | `SeedReplantEvent.SeedRepr` |
| `SinglePlot.ripenSeed` | seed ≤ 50、fruit ≤ 25 | `SeedRipenEvent.SeedRepr/FruitRepr`；下流の `SeedInputEvent` は fruit の 25 rune 表現を再利用 |
| `SplitPlot.ripenSeed` | seed ≤ 50、`[]F` ≤ 25；各要素 ≤ 50 | スライスは `SeedRipenEvent.FruitRepr` に、単一の要素は下流の `SeedInputEvent.SeedRepr` に使用 |
| `RoutePlot.ripenSeed` | seed ≤ 50、ルーティングテーブル ≤ 25；単一の yield ≤ 50 | ルーティングテーブルは `SeedRipenEvent.FruitRepr` に、単一の yield は下流の `SeedInputEvent.SeedRepr` に使用 |

> 「結果」は一律 25 rune（長くなる可能性があるため識別できれば十分）、「seed / 単一の yield」は一律 50 rune（識別しやすさの方が重要）というのが、パッケージ内で慣習的に決まった役割分担です。

> `persist.LogWatcher` はイベント内の `SeedRepr` / `FruitRepr` を直接使ってログを描画するため、`trunc` の効果は最終的にログテキストに現れます。ログはイベント ID も保持しているため、完全なデータを引き続き追跡できます。

## 注意事項

- `trunc` は非公開関数であり、外部パッケージから **直接呼び出せません**。文字列を切り詰めたい場合は、自分で実装するか、`pkg/persist` 内ですでにラップされている処理を再利用してください。
- `trunc` は結果に「元の完全な長さ」の情報を残しません。デバッグ時に完全な内容が必要な場合は、テストまたは開発モードでカスタム `Watcher` を使ってイベントの `Seed` / `Fruit` の生フィールドを読み取ることを推奨します。
- 元の文字列が `len(runes) <= maxLen` の場合、関数は `s + "..."` を **返さず**、そのまま返します。つまりちょうど `maxLen` に等しい場合も省略記号は付きません。
- ライフサイクルのレコード（`LifecycleRecord.SeedJSON` / `FruitJSON`）は `trunc` を **経由しません**。`persist.LifecycleWatcher` がシリアライズするのはイベント内の元の `Seed` / `Fruit` であり、常に完全な JSON を保存します。切り詰めは可読表現にのみ影響します。
