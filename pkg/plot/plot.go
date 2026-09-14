package plot

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
)

// ==== Interface ====

// PlotNode 是 Farm 管理 plot 时使用的统一接口。
// 它擦除了泛型参数，使 Farm 可以用同一类型持有不同种子/果实类型的 Plot。
// 接口覆盖图连接、运行装配
// 以及执行控制（StartAsync、WaitAsync、SeedAny、Seal）所需的最小能力。
type PlotNode interface {
	GetName() string
	GetState() int32
	GetSeedChanAny() any

	ConnectTo(next PlotNode) error
	SetUpstreamYieldCounter(name string, yieldCounter *atomic.Int64)
	BindInlet(logChan chan<- persist.LogRecord, lifecycleChan chan<- persist.LifecycleRecord)
	SetEventClient(eventClient runtime.EventClient)

	StartAsync()
	WaitAsync()
	SeedAny(seed any) error
	Seal()
}

// ==== Struct ====

// Plot 是标准的可连接并发种子培育节点。
// 它复用 basePlot 共享骨架，并沿用当前“一颗 fruit 转发为一颗下游 yield”的成功路径。
// S 为种子类型，F 为果实类型。
type Plot[S any, F any] struct {
	*basePlot[S, F, F]
}

// ==== Construction ====

// NewPlot 创建一个 Plot 实例。
// name 为 plot 名称（在 Farm 中需唯一），cultivator 为培育函数，
// opts 为可选配置项。
func NewPlot[S any, F any](name string, cultivator func(S) (F, error), opts ...Option) *Plot[S, F] {
	var p *Plot[S, F]
	base := newBasePlot[S, F, F](name, cultivator, func(seedPayload runtime.Payload[S], fruit F, startTime time.Time) {
		p.ripenSeed(seedPayload, fruit, startTime)
	}, opts...)
	p = &Plot[S, F]{basePlot: base}
	return p
}

// ==== Result Handling ====

// ripenSeed 处理培育成功的种子：更新计数、记录日志、推进生命周期并发送果实。
func (p *Plot[S, F]) ripenSeed(seedPayload runtime.Payload[S], fruit F, startTime time.Time) {
	p.AddFruitNum(1)
	p.reportProgress()

	seed := seedPayload.Value
	seedID := seedPayload.EventID
	fruitID := p.eventClient.Emit("fruit", []int{seedID})

	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	fruitRepr := trunc(fmt.Sprintf("%+v", fruit), 25)
	useTime := time.Since(startTime).Seconds()
	p.logInlet.SeedRipen(p.name, seedRepr, fruitRepr, useTime, seedID, fruitID)
	p.lifecycleInlet.SeedRipen(p.name, seedID, seedID, fruitID, fruit)

	for nextPlot, ch := range p.yieldChans {
		p.AddDownstreamYieldNum(nextPlot, 1)
		downstreamSeedID := p.eventClient.Emit("seed", []int{fruitID})
		p.lifecycleInlet.SeedIn(nextPlot, downstreamSeedID, []int{fruitID}, fruit)
		yieldPayload := runtime.Payload[F]{Value: fruit, EventID: downstreamSeedID}
		ch <- yieldPayload
	}
}
