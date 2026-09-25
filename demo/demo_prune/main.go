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

	p := grow.NewPlot("single_dedup", double,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)
	p.Run(seeds)

	fmt.Printf("[single plot] input=%v\n", seeds)
	fmt.Printf("[single plot] fruit=%d (去重后翻倍), prune=%d (重复被修剪), weed=%d\n",
		p.GetFruitNum(), p.GetPruneNum(), p.GetWeedNum())
}

// farmDedup 多 plot 场景：root 翻倍产生重复值，head 去重后加一。
func farmDedup() {
	root := grow.NewPlot("double", double, grow.WithTenders(3))
	head := grow.NewPlot("dedup_add", addOne,
		grow.WithTenders(3),
		grow.WithPruneIf(newDeduplicator().pruneIf),
	)

	farm := grow.NewFarm("farm_dedup", "INFO")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"double": {1, 2, 2, 3, 3, 3},
	}); err != nil {
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