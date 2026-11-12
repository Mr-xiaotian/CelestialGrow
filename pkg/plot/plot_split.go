package plot

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// ==== Struct ====

// SplitPlot 是将单个 seed 拆分为多个下游 yield 的并发节点。
// 它复用 basePlot 共享骨架，并将当前节点成功结果定义为 []F，
// 再把其中每个元素作为单独的下游 yield 转发。
// S 为种子类型，F 为拆分后的下游元素类型。
type SplitPlot[S any, F any] struct {
	*basePlot[S, []F, F]
}

// ==== Construction ====

// NewSplitPlot 创建一个 SplitPlot 实例。
// name 为 plot 名称（在 Farm 中需唯一），splitter 为拆分函数，
// opts 为可选配置项。
func NewSplitPlot[S any, F any](name string, splitter func(S) ([]F, error), opts ...Option) *SplitPlot[S, F] {
	// 先声明 p 再构造 base：ripen 钩子闭包捕获 p，但该闭包只在
	// StartAsync 启动后的 sprout/tend 路径中被调用，彼时 p 已完成赋值。
	var p *SplitPlot[S, F]
	base := newBasePlot[S, []F, F](name, splitter, func(seedPayload runtime.Payload[S], fruits []F, startTime time.Time) {
		p.ripenSeed(seedPayload, fruits, startTime)
	}, opts...)
	p = &SplitPlot[S, F]{basePlot: base}
	return p
}

// ==== Result Handling ====

// ripenSeed 处理拆分成功的种子：发出种子成熟事件，并逐项向下游投递产出。
func (p *SplitPlot[S, F]) ripenSeed(seedPayload runtime.Payload[S], fruits []F, startTime time.Time) {
	seed := seedPayload.Value
	seedID := seedPayload.EventID
	fruitID := p.eventClient.Emit("fruit", []int{seedID})

	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	fruitRepr := trunc(fmt.Sprintf("%+v", fruits), 25)
	p.emit(scarecrow.SeedRipenEvent{
		Plot:      p.name,
		Seed:      seed,
		SeedRepr:  seedRepr,
		Fruit:     fruits,
		FruitRepr: fruitRepr,
		Elapsed:   time.Since(startTime),
		SeedID:    seedID,
		FruitID:   fruitID,
		Time:      time.Now(),
	})

	for nextPlot, ch := range p.yieldChans {
		for _, fruit := range fruits {
			downstreamSeedID := p.eventClient.Emit("seed", []int{fruitID})
			yieldRepr := trunc(fmt.Sprintf("%+v", fruit), 50)

			p.emit(scarecrow.SeedInputEvent{
				Plot:     nextPlot,
				From:     p.name,
				Seed:     fruit,
				SeedRepr: yieldRepr,
				SeedID:   downstreamSeedID,
				Parents:  []int{fruitID},
				Time:     time.Now(),
			})
			yieldPayload := runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}
			ch <- yieldPayload
		}
	}
}
