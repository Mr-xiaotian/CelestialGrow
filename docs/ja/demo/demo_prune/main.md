# demo/demo_prune/main.go

> 📅 最終更新日: 2026/11/13

## 役割

`demo/demo_prune/main.go` は CelestialGrow プロジェクトで **prune（剪定）機構** を実演するサンプルで、`pkg/api` が提供する `WithPruneIf` を使って seed の並行安全な**重複排除**を行う方法を示します。重複した seed は cultivator に入る前に `prune` としてマークされ、育成されず、下流の fruit も生成しません。

[`demo_farm/main.go`](../demo_farm/main.md) と比較すると、本サンプルは 2 点に焦点を当てています。

- **単一 plot のシナリオ**（`singlePlotDedup`）：重複値を含む seed リストを直接重複排除してから 2 倍にします。
- **複数 plot のシナリオ**（`farmDedup`）：root が 2 倍にして重複値を生成し、head が重複排除しながら 1 を加えます。

どちらの経路も `WithPruneIf` 述語を再利用しており、接続位置が異なるだけです。

## コード構造

ファイルは 4 つの部分から構成されています。

1. **パッケージとインポート**：`package main` 形式で実行可能なエントリを提供し、`fmt`、`sync`、`pkg/api`（別名 `grow`）をインポートします。
2. **`deduplicator` 重複排除器**：内部で `sync.Mutex` で保護された `map[int]struct{}` を使って「既読 seed」集合を維持し、`pruneIf` 述語メソッドを提供します。
3. **2 つの cultivator と 1 つの述語メソッド**：`double`、`addOne` を cultivator とし、`deduplicator.pruneIf` を `WithPruneIf` 述語とします。
4. **2 つのシナリオ + `main`**：`singlePlotDedup` と `farmDedup` がそれぞれ単一 / 複数 plot の重複排除を示し、`main` が順に両方を呼び出します。

## 主要なオブジェクト

### `deduplicator`

```go
// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}
```

- `pruneIf` の戻り値は `WithPruneIf` にそのまま採用されます。`true` はその seed を剪定すべきこと、`false` は育成を続けることを意味します。
- ロックにより、複数の tender が並行する場合でも「1 回だけ処理する」ことを保証します。2 つのコルーチンが同時に同じ seed に出会っても、`false` の返却と登録を原子的に完了できるのは 1 つだけで、もう一方は `true` を受け取ります。

## 主要な API

本サンプルで使用する関連 API（構築と設定は `pkg/api` 由来、計数メソッドは `pkg/plot` の `basePlot` が提供）：

| 呼び出し | 由来 | 役割 |
|------|------|------|
| `grow.WithPruneIf[S any](fn func(S) bool)` | `pkg/api` → `plot.WithPruneIf` | Plot に剪定述語を注入します。cultivator の前に各 seed を評価し、該当すれば育成をスキップします。 |
| `grow.NewSinglePlot[S, F](name, cultivator, opts...)` | `pkg/api` → `plot.NewSinglePlot` | 1 入力 1 出力ノードを作成します。`WithPruneIf` は `opts` を通じて注入します。 |
| `p.GetPruneNum() int` | `pkg/plot` の `basePlot` メソッド（`SinglePlot` 経由で昇格） | この Plot で累計剪定された seed 数を読み取ります。 |
| `p.GetFruitNum() int` / `p.GetWeedNum() int` | 同上 | fruit / weed の計数を読み取り、`GetPruneNum` と合わせて全体の状態を示します。 |

> これらの計数メソッドは `MetricsWatcher` の指標スナップショットを読み取ります。`SinglePlot.Run` と `Farm.Run` はどちらも `pkg/assembly` を通じて `MetricsWatcher` を自動登録するため、実行終了後に直接読み取れます。指標オブザーバーが組み立てられていない場合は `0` を返します。

## シナリオ 1：`singlePlotDedup`

```go
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")
```

- 8 個の seed を入力し、そのうち `1`、`2`、`4` がそれぞれ重複して出現します。
- `WithPruneIf` は「既読集合」を維持し、最初に出現した 5 個の異なる値が育成され（重複排除後に 2 倍）、重複した 3 個が剪定されます。
- `p.Run` は single-plot の同期実行エントリで、Farm は不要です。第 2 引数はログレベルです。
- 出力：`fruit=5（重複排除後に 2 倍）, prune=3（重複が剪定）, weed=0`。

## シナリオ 2：`farmDedup`

```go
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}
```

- `root` は 6 個の seed を 2 倍にして `head` へ転送し、`2, 4, 4, 6, 6, 6` を生成します。
- `head` はこの 6 個の fruit（自身の seed）を重複排除します。`2, 4, 6` が育成されてそれぞれ 1 が加算され、`3, 5, 7` になります。重複した 3 個は剪定されます。
- 出力：`dedup_add: fruit=3（重複排除 +1 -> 3,5,7）, prune=3（重複が剪定）, weed=0`。

## 実行方法

プロジェクトのルートディレクトリで実行します。

```bash
go run ./demo/demo_prune
```

実行に成功すると、ターミナルに 4 行の概要が出力されます。

```text
[single plot] input=[1 2 3 2 1 4 4 5]
[single plot] fruit=5 (去重后翻倍), prune=3 (重复被修剪), weed=0
[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)
[farm] dedup_add: fruit=3 (去重+1 -> 3,5,7), prune=3 (重复被修剪), weed=0
```

同時にカレントワーキングディレクトリに実行時の生成物が作成されます。

- `logs/grow_log(YYYY-MM-DD).log`：2 回の `Run` のログが日ごとに同じファイルへ追記され、ログレベルはいずれも `"INFO"` です。
- `lifecycles/YYYY-MM-DD/`：`singlePlotDedup` と `farmDedup` がそれぞれ 1 回ずつ `Run` を呼び出すため、それぞれが 1 つの `grow_lifecycle(HH-MM-SS.mmm).sqlite3` を生成し、このディレクトリに 2 つのライフサイクルデータベースが現れます。

## 完全なソースコード

以下のコードは `demo/demo_prune/main.go` のソースと完全に一致しており、そのままコピーして実行できます。

```go
package main

import (
	"fmt"
	"sync"

	grow "github.com/Mr-xiaotian/CelestialGrow/pkg/api"
)

// deduplicator 维护一个并发安全的已见种子集合。
type deduplicator struct {
	mu   sync.Mutex
	seen map[int]struct{}
}

// newDeduplicator 创建一个空的去重器。
func newDeduplicator() *deduplicator {
	return &deduplicator{seen: make(map[int]struct{})}
}

// pruneIf 生成 WithPruneIf 谓词：种子已见过则修剪（跳过），
// 否则登记为已见并继续培育。加锁保证多 tender 并发下仍只处理一次。
func (d *deduplicator) pruneIf(seed int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[seed]; ok {
		return true
	}
	d.seen[seed] = struct{}{}
	return false
}

// double 将种子翻倍。
func double(num int) (int, error) {
	return num * 2, nil
}

// addOne 为种子加一。
func addOne(num int) (int, error) {
	return num + 1, nil
}

// singlePlotDedup 单 plot 场景：对含重复值的种子列表去重后翻倍。
func singlePlotDedup() {
	seeds := []int{1, 2, 3, 2, 1, 4, 4, 5}

	p := grow.NewSinglePlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds, "INFO")

	fmt.Printf("[single plot] input=%v\n", seeds)
	fmt.Printf("[single plot] fruit=%d (去重后翻倍), prune=%d (重复被修剪), weed=%d\n",
		p.GetFruitNum(), p.GetPruneNum(), p.GetWeedNum())
}

// farmDedup 多 plot 场景：root 翻倍产生重复值，head 去重后加一。
func farmDedup() {
	root := grow.NewSinglePlot("double", double, grow.WithTenders(3))
	head := grow.NewSinglePlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}, "INFO"); err != nil {
		panic(err)
	}

	fmt.Println("[farm] double:   seed=6 -> fruit=6 (翻倍转发 2,4,4,6,6,6)")
	fmt.Printf("[farm] dedup_add: fruit=%d (去重+1 -> 3,5,7), prune=%d (重复被修剪), weed=%d\n",
		head.GetFruitNum(), head.GetPruneNum(), head.GetWeedNum())
}

// main 演示用 prune 机制做种子去重：单 plot 与 farm 两个场景。
func main() {
	singlePlotDedup()
	farmDedup()
}
```

## 注意事項

1. **述語は cultivator より優先される**：`pruneIf` は `tend` 内部で、cultivator を呼び出す**前に**評価されます。該当すると `pruneSeed` の経路に入り、ビジネスロジックは実行されず、下流への出力も行われません。
2. **並行安全は呼び出し側の責任**：`WithPruneIf` は述語を評価するだけで、述語内部の状態の並行安全は保証しません。本サンプルでは `sync.Mutex` で保護した `deduplicator` を使って、複数の tender でも 1 回だけ処理されるようにしています。
3. **剪定も完了に計上される**：剪定された seed は `GetCompleted`（`fruit + weed + prune`）と `pruneNum` の両方に計上されるため、「失敗」とは区別されます。剪定は設計上の能動的なスキップであり、エラーではありません。
4. **永続化で可視化される**：剪定された seed はライフサイクルログ / データベースに `SeedPruneEvent` イベントを残し、状態は `prune` で、`ripen` / `wither` と並びます。
5. **述語はこの階層の seed にのみ作用する**：`WithPruneIf` は Plot が受け取る各入力 seed に対して評価されます。ルートノードも重複排除したい場合は、ルート Plot を作成するときにも同じ述語を渡す必要があります。
