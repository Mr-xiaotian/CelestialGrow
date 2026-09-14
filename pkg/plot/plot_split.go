package plot

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
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
	var p *SplitPlot[S, F]
	base := newBasePlot[S, []F, F](name, splitter, func(seedPayload runtime.Payload[S], fruits []F, startTime time.Time) {
		p.ripenSeed(seedPayload, fruits, startTime)
	}, opts...)
	p = &SplitPlot[S, F]{basePlot: base}
	return p
}

// ==== Result Handling ====

// ripenSeed 处理拆分成功的种子：更新计数、记录日志、推进生命周期并逐项发送下游产出。
func (p *SplitPlot[S, F]) ripenSeed(seedPayload runtime.Payload[S], fruits []F, startTime time.Time) {
	p.AddFruitNum(1)
	p.reportProgress()

	seed := seedPayload.Value
	seedID := seedPayload.EventID
	fruitID := p.eventClient.Emit("fruit", []int{seedID})

	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	fruitRepr := trunc(fmt.Sprintf("%+v", fruits), 25)
	useTime := time.Since(startTime).Seconds()
	p.logInlet.SeedRipen(p.name, seedRepr, fruitRepr, useTime, seedID, fruitID)
	p.lifecycleInlet.SeedRipen(p.name, seedID, seedID, fruitID, fruits)

	for nextPlot, ch := range p.yieldChans {
		p.AddDownstreamYieldNum(nextPlot, len(fruits))
		for _, fruit := range fruits {
			downstreamSeedID := p.eventClient.Emit("seed", []int{fruitID})
			p.lifecycleInlet.SeedIn(nextPlot, downstreamSeedID, []int{fruitID}, fruit)
			yieldPayload := runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}
			ch <- yieldPayload
		}
	}
}
