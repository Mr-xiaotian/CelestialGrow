package plot

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// ==== Struct ====

// SinglePlot 是标准的单进单出种子培育节点。
// 它复用 basePlot 共享骨架，并沿用当前“一颗 fruit 转发为一颗下游 yield”的成功路径。
// S 为种子类型，F 为果实类型。
type SinglePlot[S any, F any] struct {
	*basePlot[S, F, F]
}

// ==== Construction ====

// NewSinglePlot 创建一个 SinglePlot 实例。
// name 为 plot 名称（在 Farm 中需唯一），cultivator 为培育函数，
// opts 为可选配置项。
func NewSinglePlot[S any, F any](name string, cultivator func(S) (F, error), opts ...Option) *SinglePlot[S, F] {
	// 先声明 p 再构造 base：ripen 钩子闭包捕获 p，但该闭包只在
	// StartAsync 启动后的 sprout/tend 路径中被调用，彼时 p 已完成赋值。
	var p *SinglePlot[S, F]
	base := newBasePlot[S, F, F](name, cultivator, func(seedPayload runtime.Payload[S], fruit F, startTime time.Time) {
		p.ripenSeed(seedPayload, fruit, startTime)
	}, opts...)
	p = &SinglePlot[S, F]{basePlot: base}
	return p
}

// ==== Result Handling ====

// ripenSeed 处理培育成功的种子：发出种子成熟事件，并逐颗向下游投递果实。
func (p *SinglePlot[S, F]) ripenSeed(seedPayload runtime.Payload[S], fruit F, startTime time.Time) {
	seed := seedPayload.Value
	seedID := seedPayload.EventID
	fruitID := p.eventClient.Emit("fruit", []int{seedID})

	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	fruitRepr := trunc(fmt.Sprintf("%+v", fruit), 25)
	p.emit(scarecrow.SeedRipenEvent{
		Plot:      p.name,
		Seed:      seed,
		SeedRepr:  seedRepr,
		Fruit:     fruit,
		FruitRepr: fruitRepr,
		Elapsed:   time.Since(startTime),
		SeedID:    seedID,
		FruitID:   fruitID,
		Time:      time.Now(),
	})

	for nextPlot, ch := range p.yieldChans {
		downstreamSeedID := p.eventClient.Emit("seed", []int{fruitID})

		p.emit(scarecrow.SeedInputEvent{
			Plot:     nextPlot,
			From:     p.name,
			Seed:     fruit,
			SeedRepr: fruitRepr,
			SeedID:   downstreamSeedID,
			Parents:  []int{fruitID},
			Time:     time.Now(),
		})
		yieldPayload := runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}
		ch <- yieldPayload
	}
}
