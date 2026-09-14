package plot

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
)

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

	eventClient runtime.EventClient
	observers   []observer.Observer

	logSpout       *funnel.Spout[persist.LogRecord]
	lifecycleSpout *funnel.Spout[persist.LifecycleRecord]
	logInlet       *persist.LogInlet
	lifecycleInlet *persist.LifecycleInlet

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	state  atomic.Int32 // 0=idle, 1=running, 2=done
	*Counter
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

		eventClient: runtime.NewLocalEventClient(),

		ctx:     ctx,
		cancel:  cancel,
		Counter: NewCounter(),
	}
}

// ==== Observer Registration ====

// AddObserver 添加一个进度观察者。
func (p *basePlot[S, F, Y]) AddObserver(observer observer.Observer) {
	p.observers = append(p.observers, observer)
}

// ==== Initialization ====

// BindInlet 绑定日志和生命周期记录的写入通道。
// standalone 模式由 Run 创建本地 spout 后调用；
// Farm 模式由 Farm.Run 统一调用。
func (p *basePlot[S, F, Y]) BindInlet(logChan chan<- persist.LogRecord, lifecycleChan chan<- persist.LifecycleRecord) {
	p.logInlet = persist.NewLogInlet(logChan, time.Second, p.logLevel)
	p.lifecycleInlet = persist.NewLifecycleInlet(lifecycleChan, time.Second)
}

// StartSpouts 启动本地日志/生命周期 spout。仅 standalone 模式使用。
func (p *basePlot[S, F, Y]) StartSpouts() {
	p.logSpout.Start()
	p.lifecycleSpout.Start()
}

// StopSpouts 停止本地日志/生命周期 spout 并刷盘。仅 standalone 模式使用。
func (p *basePlot[S, F, Y]) StopSpouts() {
	p.logSpout.Stop()
	p.lifecycleSpout.Stop()
}

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

	downstreamYield := &atomic.Int64{}
	p.SetDownstreamYieldCounter(next.GetName(), downstreamYield)
	next.SetUpstreamYieldCounter(p.GetName(), downstreamYield)
	return nil
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

// ==== Observer Hooks ====

// reportProgress 通知所有 Observer 当前进度。
func (p *basePlot[S, F, Y]) reportProgress() {
	completed := p.GetCompleted()
	seedNum := p.GetSeedNum()
	for _, observer := range p.observers {
		observer.OnProgress(completed, seedNum)
	}
}

// notifyStart 将状态设为 running 并通知所有 Observer。
func (p *basePlot[S, F, Y]) notifyStart() {
	p.state.Store(1)
	seedNum := p.GetSeedNum()
	for _, observer := range p.observers {
		observer.OnStart(seedNum)
	}
}

// notifyFinish 将状态设为 done 并通知所有 Observer。
func (p *basePlot[S, F, Y]) notifyFinish() {
	p.state.Store(2)
	completed := p.GetCompleted()
	seedNum := p.GetSeedNum()
	for _, observer := range p.observers {
		observer.OnFinish(completed, seedNum)
	}
}

// ==== Result Handling ====

// witherSeed 处理培育失败的种子：更新计数、记录日志并推进生命周期。
func (p *basePlot[S, F, Y]) witherSeed(seedPayload runtime.Payload[S], err error, startTime time.Time) {
	p.AddWeedNum(1)
	p.reportProgress()

	seed := seedPayload.Value
	seedID := seedPayload.EventID
	seedString := fmt.Sprintf("%+v", seed)
	weedID := p.eventClient.Emit("weed", []int{seedID})

	seedRepr := trunc(seedString, 50)
	useTime := time.Since(startTime).Seconds()
	p.logInlet.SeedWither(p.name, seedRepr, err, useTime, seedID, weedID)
	p.lifecycleInlet.SeedWither(p.name, seedID, seedID, weedID, err)
}

// ==== Internal Pipeline ====

// sprout 调度器：从 seedChan 读取种子，分发给 tender 协程并行处理。
// 通过信号量控制最大并发数，并根据 seal 信号判断输入是否结束。
// 外部 input seal 具有强终止语义：一旦收到，即不再等待剩余上游 seal。
// 所有已接收种子处理完毕后，向所有 yieldChans 发送 SignalSeal 通知下游。
func (p *basePlot[S, F, Y]) sprout() {
	sem := make(chan struct{}, p.numTenders)
	done := make(chan struct{}, p.numTenders)
	sealedFrom := make(map[string]int, len(p.upstreamYields))

	ctxCancel := false
	inputClosed := false
	inFlight := 0
	shouldFinish := func() bool {
		return ctxCancel || (inputClosed && inFlight == 0)
	}

	for {
		if shouldFinish() {
			patents := make([]int, 0, len(p.upstreamYields))
			for _, sealID := range sealedFrom {
				patents = append(patents, sealID)
			}
			sealID := p.eventClient.Emit("seal", patents)
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
	if _, ok := p.upstreamYields[source]; !ok {
		return false
	}
	sealedFrom[source] = sealID
	return len(sealedFrom) == len(p.upstreamYields)
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
	seedRepr := trunc(fmt.Sprintf("%+v", seedPayload.Value), 50)

	var fruit F
	var err error
	seed := seedPayload.Value
	seedID := seedPayload.EventID

	for attempt := 1; attempt <= p.maxRetries+1; attempt++ {
		fruit, err = p.cultivator(seed)
		if err == nil {
			break
		}
		if !p.retryIf(err) {
			break
		}
		if attempt <= p.maxRetries {
			p.logInlet.SeedReplant(p.name, seedRepr, attempt, err, seedID)
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
// 供 Farm 统一注入初始任务时使用。
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

	p.logInlet.SeedInput(p.name, seedRepr, seedID)
	p.lifecycleInlet.SeedInput(p.name, seedID, nil, seed)
	p.seedChan <- runtime.Payload[S]{Value: seed, EventID: seedID}

	p.AddSeedNum(1)
}

// Seal 向 seedChan 发送来自外部 input 的 SignalSeal。
// 这表示外部调用者要求终止该 plot 的后续输入处理；对于已连接上游的
// plot，这同样会触发强终止，而不是继续等待剩余上游 seal。
func (p *basePlot[S, F, Y]) Seal() {
	sealID := p.eventClient.Emit("seal", []int{})
	p.seedChan <- runtime.Payload[S]{Signal: runtime.SignalSeal, Source: sourceInput, EventID: sealID}
}

// StartAsync 异步启动 sprout 调度器。
// 调用前需先完成 BindInlet 绑定通道。
// standalone 模式下通常由外部通过 Seed/Seal 控制输入；
// Farm 模式下则由 Farm 注入初始种子并接收上游转发的 yield。完成后需调用 WaitAsync 等待退出。
func (p *basePlot[S, F, Y]) StartAsync() {
	p.wg.Go(func() {
		p.logInlet.PlotStart(p.name, p.numTenders)
		startTime := time.Now()

		p.notifyStart()
		p.sprout()
		p.notifyFinish()

		p.logInlet.PlotEnd(p.name, time.Since(startTime).Seconds(), p.GetFruitNum(), p.GetWeedNum())
	})
}

// WaitAsync 等待异步 Plot 的后台协程退出。
func (p *basePlot[S, F, Y]) WaitAsync() {
	p.wg.Wait()
}

// ==== Standalone Execution & Harvest ====

// Run 在 standalone 模式下启动 Plot 并处理所有种子。
// 它会创建本地日志/生命周期 spout，绑定 inlet，并在所有输入完成后阻塞等待退出。
func (p *basePlot[S, F, Y]) Run(seeds []S) {
	p.logSpout = funnel.NewSpout(&persist.LogRecordHandler{}, 100, time.Second)
	p.lifecycleSpout = funnel.NewSpout(&persist.LifecycleRecordHandler{}, 100, time.Second)
	p.BindInlet(p.logSpout.GetQueue(), p.lifecycleSpout.GetQueue())

	p.StartSpouts()
	defer p.StopSpouts()

	p.StartAsync()
	for _, seed := range seeds {
		p.Seed(seed)
	}
	p.Seal()
	p.WaitAsync()
}

// Harvest 读取当前 plot 已持久化的全部任务状态快照。
func (p *basePlot[S, F, Y]) Harvest() ([]persist.LifecycleStatusRecord, error) {
	if p.lifecycleSpout == nil {
		return nil, fmt.Errorf("plot %q lifecycle spout is nil", p.name)
	}

	queryHandler, ok := p.lifecycleSpout.Handler().(interface {
		LoadStatuses(plotName string) ([]persist.LifecycleStatusRecord, error)
	})
	if !ok {
		return nil, fmt.Errorf("plot %q lifecycle handler does not support status queries", p.name)
	}

	return queryHandler.LoadStatuses(p.name)
}
