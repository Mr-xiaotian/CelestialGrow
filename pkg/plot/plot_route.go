package plot

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
)

// ==== Struct ====

// RoutePlot 是将单个 seed 定向转发到指定下游的并发节点。
// 它复用 basePlot 共享骨架，并将当前节点成功结果定义为 map[string]Y，
// 其中 key 为目标下游名称，value 为发送给该下游的 yield。
// 因此不同下游可以收到不同 yield，未被路由到的下游不会收到任何数据。
// S 为种子类型，Y 为下游 yield 类型。
type RoutePlot[S any, Y any] struct {
	*basePlot[S, map[string]Y, Y]
}

// ==== Construction ====

// NewRoutePlot 创建一个 RoutePlot 实例。
// name 为 plot 名称（在 Farm 中需唯一），cultivator 为返回路由表的培育函数，
// opts 为可选配置项。
func NewRoutePlot[S any, Y any](name string, cultivator func(S) (map[string]Y, error), opts ...Option) *RoutePlot[S, Y] {
	// 先声明 p 再构造 base：ripen 钩子闭包捕获 p，但该闭包只在
	// StartAsync 启动后的 sprout/tend 路径中被调用，彼时 p 已完成赋值。
	var p *RoutePlot[S, Y]
	base := newBasePlot[S, map[string]Y, Y](name, cultivator, func(seedPayload runtime.Payload[S], routes map[string]Y, startTime time.Time) {
		p.ripenSeed(seedPayload, routes, startTime)
	}, opts...)
	p = &RoutePlot[S, Y]{basePlot: base}
	return p
}

// ==== Result Handling ====

// ripenSeed 处理路由成功的种子：更新计数、记录日志、推进生命周期，
// 并按路由表将 yield 定向发送给对应下游。
// 路由表中未连接的下游会被跳过，不影响其余路由项。
func (p *RoutePlot[S, Y]) ripenSeed(seedPayload runtime.Payload[S], routes map[string]Y, startTime time.Time) {
	p.AddFruitNum(1)
	p.reportProgress()

	seed := seedPayload.Value
	seedID := seedPayload.EventID
	fruitID := p.eventClient.Emit("fruit", []int{seedID})

	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	routesRepr := trunc(fmt.Sprintf("%+v", routes), 25)
	useTime := time.Since(startTime).Seconds()
	p.logInlet.SeedRipen(p.name, seedRepr, routesRepr, useTime, seedID, fruitID)
	p.lifecycleInlet.SeedRipen(p.name, seedID, seedID, fruitID, routes)

	for nextPlot, yield := range routes {
		ch, ok := p.yieldChans[nextPlot]
		if !ok {
			continue
		}

		p.AddDownstreamYieldNum(nextPlot, 1)
		downstreamSeedID := p.eventClient.Emit("seed", []int{fruitID})
		yieldRepr := trunc(fmt.Sprintf("%+v", yield), 50)

		p.logInlet.SeedInput(nextPlot, yieldRepr, downstreamSeedID)
		p.lifecycleInlet.SeedInput(nextPlot, downstreamSeedID, []int{fruitID}, yield)
		yieldPayload := runtime.Payload[Y]{Value: yield, EventID: downstreamSeedID}
		ch <- yieldPayload
	}
}
