# pkg/plot/helper.go

> 📅 最終更新日: 2026/09/24

`helper.go` は `plot` パッケージ内の非公開ユーティリティ関数の集合です。現在は文字列を切り詰める関数 `trunc` を 1 つだけ提供しており、ログを書く前に seed / fruit / yield の文字列表現を短い形式に圧縮し、ログ行が長くなりすぎるのを避けるために使われます。

## 役割

- 「先頭と末尾をそれぞれ 1/3 ずつ残し、中間を `...` で置き換える」文字列の切り詰め機能を提供します。
- `basePlot` と 3 種類のノードが `logInlet.*` を呼び出す前に、`fmt.Sprintf("%+v", value)` の結果を短く圧縮するために使用します。

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
| `string` | `maxLen` rune を超えない文字列。超える場合は `<先頭 1/3>...<末尾 1/3>` の形になる |

## 動作の詳細

- **rune 単位で数える**：`[]rune(s)` への変換を使用し、中国語などのマルチバイト文字が途中で切れるのを避けます。
- **`max(1, maxLen/3)` によるフォールバック**：`maxLen < 3` の場合でも `segmentLen >= 1` を保証し、「切り詰め後に空文字列になる」事態を避けます。
- **元の文字列を変更しない**：`s` は値で渡され、`runes` を読み取るだけです。

## `plot` パッケージ内での使用箇所

現在の `trunc` の呼び出し箇所は `basePlot` の骨組みと 3 種類のノードの成功 / 失敗パスに分布しており、属するログのセマンティクスごとに分類すると次のとおりです：

| 位置 | 呼び出し | 効果 |
|------|------|------|
| `basePlot.tend` | `trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)` | seed ≤ 50。`SeedReplant` ログに使用 |
| `basePlot.witherSeed` | `trunc(seedString, 50)` | seed ≤ 50。`SeedWither` ログに使用 |
| `basePlot.Seed` | `trunc(fmt.Sprintf("%+v", seed), 50)` | ローカルに播いた seed ≤ 50。`SeedInput` ログに使用 |
| `Plot.ripenSeed` | seed ≤ 50、fruit ≤ 25 | `SeedRipen` ログに使用。fruit は **単一の** `F` |
| `SplitPlot.ripenSeed` | seed ≤ 50、`[]F` ≤ 25。各要素は ≤ 50 | 結果のスライスは `SeedRipen` に、単一の要素は下流の `SeedInput` に使用 |
| `RoutePlot.ripenSeed` | seed ≤ 50、ルーティングテーブル ≤ 25。単一の yield ≤ 50 | ルーティングテーブルは `SeedRipen` に、単一の yield は下流の `SeedInput` に使用 |

> 「結果」は一律 25 rune（長くなる可能性があるため識別できれば十分）、「seed / 単一の yield」は一律 50 rune（識別しやすさの方が重要）というのが、パッケージ内で慣習的に決まった役割分担です。

> ログは SQLite ではテキストフィールドです。`trunc` で各ログの長さを制御することで、重要な先頭と末尾の情報を保ちつつ、大きなオブジェクト（`[]byte` や長い文字列など）がログファイルを膨らませるのを避けられます。

## 注意事項

- `trunc` は非公開関数であり、外部パッケージから **直接呼び出せません**。文字列を切り詰めたい場合は、自分で実装するか、`pkg/persist` 内ですでにラップされている処理を再利用してください。
- `trunc` は結果に「元の完全な長さ」の情報を残しません。デバッグ時に完全な内容が必要な場合は、テストや開発モードでこの関数を迂回する（例えば専用の inlet / ログ handler を用意する）ことを推奨します。
- 元の文字列が `len(runes) <= maxLen` の場合、関数は `s + "..."` を **返さず**、そのまま返します。つまりちょうど `maxLen` に等しい場合も省略記号は付きません。
- ライフサイクルのレコード（`LifecycleRecord.SeedJSON` / `FruitJSON`）は `trunc` を **経由せず**、常に完全な JSON を保存します。切り詰めはログの経路でのみ発生します。
