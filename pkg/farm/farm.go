package farm

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/assembly"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// ==== Struct ====

// Farm 管理多个 plot（Plot/SplitPlot/RoutePlot）组成的静态有向图。
// 负责 plot 注册、名称唯一性校验、超边式连接建立，
// 以及统一的观察者分发与生命周期调度。Farm 级观察者会作为一个 Hub
// 注册到每个 plot，从而同时接收 Plot 事件与 Farm 事件。
type Farm struct {
	name        string
	plots       map[string]plot.PlotNode
	sourceNodes []string
	*OrderGraph

	eventClient runtime.EventClient
	hub         *scarecrow.Hub
}

// ==== Construction ====

// NewFarm 创建一个 Farm 实例。name 为 farm 名称（用于日志标识）。
func NewFarm(name string) *Farm {
	return &Farm{
		name:       name,
		plots:      make(map[string]plot.PlotNode),
		OrderGraph: NewOrderGraph(),

		eventClient: runtime.NewLocalEventClient(),
		hub:         scarecrow.NewHub(),
	}
}

// ==== Getters ====

// PlotCount 返回已注册的 plot 数量。
func (f *Farm) PlotCount() int {
	return len(f.plots)
}

// HasPlot 返回指定名称的 plot 是否已注册。
func (f *Farm) HasPlot(name string) bool {
	_, ok := f.plots[name]
	return ok
}

// GetPlot 按名称返回已注册的 plot，未找到时 ok 为 false。
func (f *Farm) GetPlot(name string) (plot.PlotNode, bool) {
	p, ok := f.plots[name]
	return p, ok
}

// getStructureList 返回渲染成文本行列表的 Farm 结构。
func (f *Farm) getStructureList() []string {
	return RenderStructureList(f.Nodes(), f.OutEdges(), f.sourceNodes)
}

// ==== Registration ====

// AddPlot 将一个或多个 plot 注册到 farm。
// plot 名称不能为空且必须唯一；注册时会加入拓扑图并共享 Farm 的事件客户端。
func (f *Farm) AddPlot(plots ...plot.PlotNode) error {
	for _, p := range plots {
		if p == nil {
			return fmt.Errorf("plot is nil")
		}

		name := p.GetName()
		if name == "" {
			return fmt.Errorf("plot name cannot be empty")
		}
		if _, exists := f.plots[name]; exists {
			return fmt.Errorf("plot %q already exists", name)
		}

		f.plots[name] = p
		f.AddNode(name)
		p.SetEventClient(f.eventClient)
	}

	return nil
}

// requireRegistered 确保 plot 已注册到 farm 中，用于连接前校验。
func (f *Farm) requireRegistered(p plot.PlotNode) error {
	if p == nil {
		return fmt.Errorf("plot is nil")
	}
	if registered, ok := f.plots[p.GetName()]; !ok || registered != p {
		return fmt.Errorf("plot %q is not registered in farm", p.GetName())
	}
	return nil
}

// ==== Connection ====

// uniquePlots 对 plot 列表按名称去重，过滤 nil。
func uniquePlots(plots []plot.PlotNode) []plot.PlotNode {
	seen := make(map[string]struct{}, len(plots))
	unique := make([]plot.PlotNode, 0, len(plots))
	for _, p := range plots {
		if p == nil {
			continue
		}
		name := p.GetName()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, p)
	}
	return unique
}

// Connect 在源组和目标组之间建立全连接（笛卡尔积）。
// 每条连接调用 from.ConnectTo(to) 完成类型校验与产出通道接入，
// 并由 ConnectTo 内部创建对应的下游产出计数器、双向登记，
// 供 seal 聚合和种子统计使用。
func (f *Farm) Connect(fromPlots []plot.PlotNode, toPlots []plot.PlotNode) error {
	fromUnique := uniquePlots(fromPlots)
	toUnique := uniquePlots(toPlots)

	if len(fromUnique) == 0 {
		return fmt.Errorf("from plots cannot be empty")
	}
	if len(toUnique) == 0 {
		return fmt.Errorf("to plots cannot be empty")
	}

	for _, from := range fromUnique {
		if err := f.requireRegistered(from); err != nil {
			return err
		}
	}
	for _, to := range toUnique {
		if err := f.requireRegistered(to); err != nil {
			return err
		}
	}

	for _, from := range fromUnique {
		for _, to := range toUnique {
			if err := from.ConnectTo(to); err != nil {
				return err
			}
			f.AddEdge(from.GetName(), to.GetName())
		}
	}

	return nil
}

// ==== Watcher Registration ====

// AddWatcher 注册一个 Farm 级 watcher，可接收 Farm 事件与所有 Plot 事件。
// 可在 Run/Start 前注册；Hub 支持运行期追加，之后的事件才会送达新 watcher。
func (f *Farm) AddWatcher(watcher scarecrow.Watcher) error {
	return f.hub.AddWatcher(watcher)
}

// ==== Execution ====

// validateRunInputs 校验输入参数中的 plot 均已注册到 farm。
func (f *Farm) validateRunInputs(inputs map[string][]any) error {
	for name := range inputs {
		if _, ok := f.plots[name]; !ok {
			return fmt.Errorf("plot %q is not registered in farm", name)
		}
	}
	return nil
}

// emit 将事件分发给 farm 级观察者。观察者故障由 Hub 的错误处理器消费。
func (f *Farm) emit(event scarecrow.Event) {
	_ = f.hub.OnEvent(event)
}

// Run 同步运行整张 Farm：先启动所有 Plot，再注入初始种子，并向源 Plot
// 发送终止信号，最后等待全部 Plot 退出。
// logLevel 为本次运行的日志最低级别，空字符串按 INFO 处理。
func (f *Farm) Run(inputs map[string][]any, logLevel string) error {
	if err := f.validateRunInputs(inputs); err != nil {
		return err
	}

	f.sourceNodes = SourceNodes(f.OrderGraph)
	return f.start(logLevel, func() error {
		if err := f.InjectTasks(inputs); err != nil {
			return err
		}
		return f.InjectTerminations(f.sourceNodes)
	})
}

// Start 装配运行期资源并启动所有 Plot，阻塞到全部 Plot 退出。
//
// 与 Run 不同，Start 不注入任何初始种子或终止信号，适合外部驱动场景
// （如 reporter）：调用方可在 Start 阻塞期间通过 InjectTasks /
// InjectTerminations 动态控制任务流。若没有外部注入终止信号，Start 不会返回。
// logLevel 为本次运行的日志最低级别，空字符串按 INFO 处理。
func (f *Farm) Start(logLevel string) error {
	f.sourceNodes = SourceNodes(f.OrderGraph)
	return f.start(logLevel, nil)
}

// InjectTasks 向指定 Plot 注入种子。可在 Start/Run 运行期间调用。
// 所有目标 Plot 必须已注册，否则返回错误且不注入任何种子。
func (f *Farm) InjectTasks(tasks map[string][]any) error {
	if err := f.validateRunInputs(tasks); err != nil {
		return err
	}

	for name, seeds := range tasks {
		plot := f.plots[name]
		for _, seed := range seeds {
			if err := plot.SeedAny(seed); err != nil {
				return err
			}
		}
	}
	return nil
}

// InjectTerminations 向指定 Plot 注入终止信号。可在 Start/Run 运行期间调用。
// 所有目标 Plot 必须已注册，否则返回错误且不注入任何终止信号。
// 向某个 Plot 注入终止信号后，再向它注入种子将不会被处理。
func (f *Farm) InjectTerminations(nodes []string) error {
	for _, name := range nodes {
		if _, ok := f.plots[name]; !ok {
			return fmt.Errorf("plot %q is not registered in farm", name)
		}
	}

	for _, name := range nodes {
		f.plots[name].Seal()
	}
	return nil
}

// start 是 Run 与 Start 共用的执行流程。
// prepare 在 Plot 启动后、等待退出前调用，用于注入初始任务与终止信号。
func (f *Farm) start(logLevel string, prepare func() error) error {
	startTime := time.Now()

	resources, err := assembly.StartFarmResources(f.hub, logLevel)
	if err != nil {
		return err
	}
	defer resources.Stop()

	f.emit(scarecrow.FarmStartEvent{
		Farm:        f.name,
		IsDAG:       IsDAG(f.OrderGraph),
		Plots:       f.Nodes(),
		Edges:       f.OutEdges(),
		SourcePlots: f.sourceNodes,
		Structure:   f.getStructureList(),
		StartTime:   startTime,
	})
	defer func() {
		f.emit(scarecrow.FarmEndEvent{
			Farm:    f.name,
			EndTime: time.Now(),
			Elapsed: time.Since(startTime),
		})
	}()

	for _, plot := range f.plots {
		if err := plot.AddWatcher(f.hub); err != nil {
			return err
		}
	}

	for _, plot := range f.plots {
		plot.StartAsync()
	}

	var prepareErr error
	if prepare != nil {
		prepareErr = prepare()
	}
	if prepareErr != nil {
		// 注入失败时仍需终止源 Plot，避免等待未注入终止信号的图。
		_ = f.InjectTerminations(f.sourceNodes)
	}

	for _, plot := range f.plots {
		plot.WaitAsync()
	}

	return prepareErr
}
