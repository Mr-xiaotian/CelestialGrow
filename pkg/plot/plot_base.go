package plot

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
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
	AddUpstream(name string)
	AddWatcher(listener scarecrow.Watcher) error
	SetEventClient(eventClient runtime.EventClient)

	StartAsync()
	WaitAsync()
	SeedAny(seed any) error
	Seal()
}

// ==== Struct ====

// basePlot 是 Plot 及其特化节点共享的运行骨架。
// 它负责通用状态、并发调度、失败处理和上下游连接，成功路径由 ripen 钩子注入。
type basePlot[S any, F any, Y any] struct {
	name       string
	cultivator func(S) (F, error)
	ripenSeed  func(seedPayload runtime.Payload[S], fruit F, startTime time.Time)
	plotOptions

	seedChan   chan runtime.Payload[S]
	yieldChans map[string]chan runtime.Payload[Y]
	upstreams  map[string]struct{}

	eventClient runtime.EventClient
	hub         *scarecrow.Hub

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	state  atomic.Int32 // 0=idle, 1=running, 2=done
}

// ==== Construction ====

// newBasePlot 创建并返回一个共享运行骨架。
func newBasePlot[S any, F any, Y any](name string, cultivator func(S) (F, error), ripenSeed func(seedPayload runtime.Payload[S], fruit F, startTime time.Time), opts ...Option) *basePlot[S, F, Y] {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &basePlot[S, F, Y]{
		name:        name,
		cultivator:  cultivator,
		ripenSeed:   ripenSeed,
		plotOptions: o,

		seedChan:   make(chan runtime.Payload[S], o.chanSize),
		yieldChans: make(map[string]chan runtime.Payload[Y]),
		upstreams:  make(map[string]struct{}),

		eventClient: runtime.NewLocalEventClient(),
		hub:         scarecrow.NewHub(),

		ctx:    ctx,
		cancel: cancel,
	}
}

// ==== Watcher Registration ====

// AddWatcher 注册一个观察者。重复注册会重复收到事件。
func (p *basePlot[S, F, Y]) AddWatcher(listener scarecrow.Watcher) error {
	return p.hub.AddWatcher(listener)
}

// emit 将事件分发给所有观察者。观察者故障由 Hub 的错误处理器消费，不影响执行。
func (p *basePlot[S, F, Y]) emit(event scarecrow.Event) {
	_ = p.hub.OnEvent(event)
}

// ==== Initialization ====

// SetEventClient 设置 plot 的事件客户端。
func (p *basePlot[S, F, Y]) SetEventClient(eventClient runtime.EventClient) {
	p.eventClient = eventClient
}

// ==== Connection ====

// ConnectTo 将当前 plot 的产出连接到下游 plot 的种子输入。
// 通过类型断言校验上游 Y 与下游 S 是否匹配。
func (p *basePlot[S, F, Y]) ConnectTo(next PlotNode) error {
	seedChan, ok := next.GetSeedChanAny().(chan runtime.Payload[Y])
	if !ok {
		return fmt.Errorf("plot %q yield type is incompatible with plot %q seed type", p.name, next.GetName())
	}

	p.yieldChans[next.GetName()] = seedChan
	next.AddUpstream(p.name)
	return nil
}

// AddUpstream 登记一个上游 Plot，用于 seal 聚合。
// 由上游在 ConnectTo 中调用，连接建立后不再变更。
func (p *basePlot[S, F, Y]) AddUpstream(name string) {
	p.upstreams[name] = struct{}{}
}

// ==== Getters ====

// GetName 返回 plot 名称。
func (p *basePlot[S, F, Y]) GetName() string {
	return p.name
}

// GetState 返回当前状态：0=idle, 1=running, 2=done。
func (p *basePlot[S, F, Y]) GetState() int32 {
	return p.state.Load()
}

// GetSeedChanAny 以 any 类型返回 seedChan，供 Farm 连接时做类型断言。
func (p *basePlot[S, F, Y]) GetSeedChanAny() any {
	return p.seedChan
}

// ==== Metrics ====

// currentMetrics 返回当前 plot 的指标快照；未装配指标观察者时 ok 为 false。
func (p *basePlot[S, F, Y]) currentMetrics() (scarecrow.PlotMetrics, bool) {
	metrics, ok := scarecrow.Find[*scarecrow.MetricsWatcher](p.hub)
	if !ok {
		return scarecrow.PlotMetrics{}, false
	}
	return metrics.GetPlot(p.name)
}

// GetSeedNum 返回当前 plot 的输入总数，包括本地播种与上游投递。
func (p *basePlot[S, F, Y]) GetSeedNum() int {
	metrics, ok := p.currentMetrics()
	if !ok {
		return 0
	}
	return metrics.InputTotal
}

// GetFruitNum 返回成功数（果实）。
func (p *basePlot[S, F, Y]) GetFruitNum() int {
	metrics, ok := p.currentMetrics()
	if !ok {
		return 0
	}
	return metrics.Succeeded
}

// GetWeedNum 返回失败数（杂草）。
func (p *basePlot[S, F, Y]) GetWeedNum() int {
	metrics, ok := p.currentMetrics()
	if !ok {
		return 0
	}
	return metrics.Failed
}

// GetPruneNum 返回剪枝数（跳过）。
func (p *basePlot[S, F, Y]) GetPruneNum() int {
	metrics, ok := p.currentMetrics()
	if !ok {
		return 0
	}
	return metrics.Skipped
}

// GetCompleted 返回已完成数（果实 + 杂草 + 剪枝）。
func (p *basePlot[S, F, Y]) GetCompleted() int {
	metrics, ok := p.currentMetrics()
	if !ok {
		return 0
	}
	return metrics.Processed
}

// IsFinish 判断所有已输入种子是否都已得出结果。
// 未装配指标观察者时返回 false。
func (p *basePlot[S, F, Y]) IsFinish() bool {
	metrics, ok := p.currentMetrics()
	if !ok {
		return false
	}
	return metrics.Processed == metrics.InputTotal
}

// ==== Watcher Hooks ====

// notifyStart 将状态设为 running 并发出 Plot 启动事件。
func (p *basePlot[S, F, Y]) notifyStart(startTime time.Time) {
	p.state.Store(1)
	p.emit(scarecrow.PlotStartEvent{Plot: p.name, Tenders: p.numTenders, StartTime: startTime})
}

// notifyFinish 将状态设为 done 并发出 Plot 结束事件。
func (p *basePlot[S, F, Y]) notifyFinish(startTime time.Time) {
	p.state.Store(2)
	p.emit(scarecrow.PlotEndEvent{Plot: p.name, EndTime: time.Now(), Elapsed: time.Since(startTime)})
}

// ==== Result Handling ====

// witherSeed 处理培育失败的种子：发出种子枯萎事件。
func (p *basePlot[S, F, Y]) witherSeed(seedPayload runtime.Payload[S], err error, startTime time.Time) {
	seed := seedPayload.Value
	seedID := seedPayload.EventID
	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	weedID := p.eventClient.Emit("weed", []int{seedID})

	p.emit(scarecrow.SeedWitherEvent{
		Plot:     p.name,
		Seed:     seed,
		SeedRepr: seedRepr,
		Err:      err,
		Elapsed:  time.Since(startTime),
		SeedID:   seedID,
		WeedID:   weedID,
		Time:     time.Now(),
	})
}

// pruneSeed 处理剪枝种子：发出种子剪枝事件。
func (p *basePlot[S, F, Y]) pruneSeed(seedPayload runtime.Payload[S], startTime time.Time) {
	seed := seedPayload.Value
	seedID := seedPayload.EventID
	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)
	pruneID := p.eventClient.Emit("prune", []int{seedID})

	p.emit(scarecrow.SeedPruneEvent{
		Plot:     p.name,
		Seed:     seed,
		SeedRepr: seedRepr,
		Elapsed:  time.Since(startTime),
		SeedID:   seedID,
		PruneID:  pruneID,
		Time:     time.Now(),
	})
}

// ==== Internal Pipeline ====

// sprout 调度器：从 seedChan 读取种子，分发给 tender 协程并行处理。
// 通过信号量控制最大并发数，并根据 seal 信号判断输入是否结束。
// 外部 input seal 具有强终止语义：一旦收到，即不再等待剩余上游 seal。
// 所有已接收种子处理完毕后，向所有 yieldChans 发送 SignalSeal 通知下游。
func (p *basePlot[S, F, Y]) sprout() {
	sem := make(chan struct{}, p.numTenders)
	done := make(chan struct{}, p.numTenders)
	sealedFrom := make(map[string]int, len(p.upstreams))

	ctxCancel := false
	inputClosed := false
	inFlight := 0
	shouldFinish := func() bool {
		return ctxCancel || (inputClosed && inFlight == 0)
	}

	for {
		if shouldFinish() {
			parentIDs := make([]int, 0, len(p.upstreams))
			for _, sealID := range sealedFrom {
				parentIDs = append(parentIDs, sealID)
			}
			sealID := p.eventClient.Emit("seal", parentIDs)
			p.emit(scarecrow.SealMergeEvent{
				Plot:      p.name,
				ParentIDs: parentIDs,
				SealID:    sealID,
				Time:      time.Now(),
			})
			sealPayload := runtime.Payload[Y]{Signal: runtime.SignalSeal, Source: p.name, EventID: sealID}
			for _, ch := range p.yieldChans {
				ch <- sealPayload
			}
			return
		}

		select {
		case seed := <-p.seedChan:
			if seed.Signal == runtime.SignalSeal {
				inputClosed = p.markSealed(seed.Source, seed.EventID, sealedFrom)
				continue
			}

			sem <- struct{}{}
			inFlight++
			go p.tend(seed, sem, done)
		case <-done:
			inFlight--
		case <-p.ctx.Done():
			ctxCancel = true
		}
	}
}

// markSealed 处理一条 seal 信号并判断输入是否关闭。
// sourceInput 表示外部调用者显式终止该 plot 的输入，此时直接关闭；
// 否则需要等待所有已登记上游都发送过 seal 信号。
func (p *basePlot[S, F, Y]) markSealed(source string, sealID int, sealedFrom map[string]int) bool {
	if source == sourceInput {
		sealedFrom[sourceInput] = sealID
		return true
	}
	if source == "" {
		return false
	}
	if _, ok := p.upstreams[source]; !ok {
		return false
	}
	sealedFrom[source] = sealID
	return len(sealedFrom) == len(p.upstreams)
}

// tend 照料单颗种子：执行 cultivator 并在失败时按策略重试。
// 完成后通过 ripen 或 witherSeed 路由结果。
func (p *basePlot[S, F, Y]) tend(seedPayload runtime.Payload[S], sem chan struct{}, done chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			p.witherSeed(seedPayload, fmt.Errorf("cultivator panic: %v", r), time.Now())
		}
		<-sem              // 释放并发令牌
		done <- struct{}{} // 发送完成信号
	}()

	startTime := time.Now()

	var fruit F
	var err error
	seed := seedPayload.Value
	seedID := seedPayload.EventID
	seedRepr := trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)

	if p.pruneIf != nil && p.pruneIf(seed) {
		p.pruneSeed(seedPayload, startTime)
		return
	}

	for attempt := 1; attempt <= p.maxRetries+1; attempt++ {
		fruit, err = p.cultivator(seed)
		if err == nil {
			break
		}
		if !p.retryIf(err) {
			break
		}
		if attempt <= p.maxRetries {
			p.emit(scarecrow.SeedReplantEvent{
				Plot:     p.name,
				Seed:     seed,
				SeedRepr: seedRepr,
				Err:      err,
				Attempt:  attempt,
				SeedID:   seedID,
				Time:     time.Now(),
			})
		}
		time.Sleep(p.retryDelay(attempt))
	}

	if err != nil {
		p.witherSeed(seedPayload, err, startTime)
	} else {
		p.ripenSeed(seedPayload, fruit, startTime)
	}
}

// ==== Input & Async Execution ====

// SeedAny 以 any 类型播入单颗种子，内部做类型断言。
// 供 Farm 统一注入初始种子时使用。
func (p *basePlot[S, F, Y]) SeedAny(seed any) error {
	typedSeed, ok := seed.(S)
	if !ok {
		return fmt.Errorf("plot %q seed type mismatch: got %T", p.name, seed)
	}
	p.Seed(typedSeed)
	return nil
}

// Seed 播入单颗种子到 seedChan。
// 该输入视为外部调用者注入，而非来自某个上游 plot。
func (p *basePlot[S, F, Y]) Seed(seed S) {
	seedID := p.eventClient.Emit("seed", []int{})
	seedRepr := trunc(fmt.Sprintf("%+v", seed), 50)

	p.emit(scarecrow.SeedInputEvent{
		Plot:     p.name,
		Seed:     seed,
		SeedRepr: seedRepr,
		SeedID:   seedID,
		Time:     time.Now(),
	})
	p.seedChan <- runtime.Payload[S]{Value: seed, EventID: seedID}
}

// Seal 向 seedChan 发送来自外部 input 的 SignalSeal。
// 这表示外部调用者要求终止该 plot 的后续输入处理；对于已连接上游的
// plot，这同样会触发强终止，而不是继续等待剩余上游 seal。
func (p *basePlot[S, F, Y]) Seal() {
	sealID := p.eventClient.Emit("seal", []int{})
	p.emit(scarecrow.SealInputEvent{Plot: p.name, SealID: sealID, Time: time.Now()})
	p.seedChan <- runtime.Payload[S]{Signal: runtime.SignalSeal, Source: sourceInput, EventID: sealID}
}

// StartAsync 异步启动 sprout 调度器。
// 调用前可通过 AddWatcher 注册观察者，运行事件由内部 Hub 分发。
// standalone 模式下通常由外部通过 Seed/Seal 控制输入；
// Farm 模式下则由 Farm 注入初始种子、接入 farm 级观察者并接收上游转发的 yield。
// 完成后需调用 WaitAsync 等待退出。
func (p *basePlot[S, F, Y]) StartAsync() {
	p.wg.Go(func() {
		startTime := time.Now()

		p.notifyStart(startTime)
		p.sprout()
		p.notifyFinish(startTime)
	})
}

// WaitAsync 等待异步 Plot 的后台协程退出。
func (p *basePlot[S, F, Y]) WaitAsync() {
	p.wg.Wait()
}

// ==== Standalone Execution & Harvest ====

// Run 在 standalone 模式下启动 Plot 并处理所有种子。
// logLevel 为本次运行的日志最低级别，空字符串按 INFO 处理。
// 运行期资源由 assembly 统一装配，在所有输入完成后阻塞等待退出。
func (p *basePlot[S, F, Y]) Run(seeds []S, logLevel string) {
	resources, err := assembly.StartPlotResources(p.hub, logLevel)
	if err != nil {
		return
	}
	defer resources.Stop()

	p.StartAsync()
	for _, seed := range seeds {
		p.Seed(seed)
	}
	p.Seal()
	p.WaitAsync()
}

// Harvest 读取当前 plot 已持久化的全部种子状态快照。
// 生命周期观察者在调用时从 hub（含嵌套 hub）中查找，因此运行结束后可直接查询，
// 不需要在 plot 上保存数据库路径。
func (p *basePlot[S, F, Y]) Harvest() ([]persist.LifecycleStatusRecord, error) {
	lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](p.hub)
	if !ok {
		return nil, fmt.Errorf("plot %q lifecycle watcher is not registered", p.name)
	}
	dbPath := lifecycle.DBPath()
	if dbPath == "" {
		return nil, fmt.Errorf("plot %q lifecycle database is not ready", p.name)
	}
	return persist.LoadLifecycleStatusesFromPath(dbPath, p.name)
}
