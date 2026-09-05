package farm

import (
	"fmt"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/persist"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/plot"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/runtime"
)

// ==== Struct ====

// Farm 管理多个 Plot 组成的静态有向图。
// 负责节点注册、名称唯一性校验、超边式连接建立，
// 以及统一的 spout 管理和生命周期调度。
type Farm struct {
	name        string
	plots       map[string]plot.PlotNode
	sourceNodes []string
	*OrderGraph

	eventClient runtime.EventClient

	logSpout       *funnel.Spout[persist.LogRecord]
	lifecycleSpout *funnel.Spout[persist.LifecycleRecord]
	logInlet       *persist.LogInlet
	lifecycleInlet *persist.LifecycleInlet
}

// ==== Construction ====

// NewFarm 创建一个 Farm 实例。
// name 为 farm 名称（用于日志标识），logLevel 为全局日志级别。
func NewFarm(name string, logLevel string) *Farm {
	logSpout := funnel.NewSpout(&persist.LogRecordHandler{}, 100, time.Second)
	lifecycleSpout := funnel.NewSpout(&persist.LifecycleRecordHandler{}, 100, time.Second)
	logInlet := persist.NewLogInlet(logSpout.GetQueue(), time.Second, logLevel)
	lifecycleInlet := persist.NewLifecycleInlet(lifecycleSpout.GetQueue(), time.Second)

	return &Farm{
		name:       name,
		plots:      make(map[string]plot.PlotNode),
		OrderGraph: NewOrderGraph(),

		eventClient: runtime.NewLocalEventClient(),

		logSpout:       logSpout,
		lifecycleSpout: lifecycleSpout,
		logInlet:       logInlet,
		lifecycleInlet: lifecycleInlet,
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
	for _, plot := range plots {
		if plot == nil {
			return fmt.Errorf("plot is nil")
		}

		name := plot.GetName()
		if name == "" {
			return fmt.Errorf("plot name cannot be empty")
		}
		if _, exists := f.plots[name]; exists {
			return fmt.Errorf("plot %q already exists", name)
		}

		f.plots[name] = plot
		f.AddNode(name)
		plot.SetEventClient(f.eventClient)
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
	for _, plot := range plots {
		if plot == nil {
			continue
		}
		name := plot.GetName()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, plot)
	}
	return unique
}

// Connect 在源组和目标组之间建立全连接（笛卡尔积）。
// 每条连接调用 from.ConnectTo(to) 将上游产出通道接入下游 seedChan，
// 并在下游登记上游名称与产出计数器用于 seal 聚合和种子统计。
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
			to.AddUpstream(from.GetName(), from.GetYieldCounter())
			f.AddEdge(from.GetName(), to.GetName())
		}
	}

	return nil
}

// ==== Execution ====

// validateRunInputs 校验输入参数中的 plot 均已注册到 farm。
func (f *Farm) validateRunInputs(inputs map[string][]any) error {
	for name := range inputs {
		_, ok := f.plots[name]
		if !ok {
			return fmt.Errorf("plot %q is not registered in farm", name)
		}
	}
	return nil
}

// Run 同步运行整张 farm 图。
// inputs 按 plot 名称声明初始种子；这些种子会在各 plot 启动后被注入。
// 流程：计算 source 节点 → 启动全局 spout → 绑定各 plot inlet → 启动所有 plot →
// 注入种子 → 向所有 source 发送 seal → 等待所有 plot 完成 → 停止 spout。
func (f *Farm) Run(inputs map[string][]any) error {
	if err := f.validateRunInputs(inputs); err != nil {
		return err
	}

	f.sourceNodes = SourceNodes(f.OrderGraph)

	f.logSpout.Start()
	f.lifecycleSpout.Start()
	defer f.lifecycleSpout.Stop()
	defer f.logSpout.Stop()

	startTime := time.Now()
	f.logInlet.StartFarm(f.name, f.getStructureList())

	for _, plot := range f.plots {
		plot.BindInlet(f.logSpout.GetQueue(), f.lifecycleSpout.GetQueue())
	}

	for _, plot := range f.plots {
		plot.StartAsync()
	}

	for name, seeds := range inputs {
		plot := f.plots[name]
		for _, seed := range seeds {
			if err := plot.SeedAny(seed); err != nil {
				return err
			}
		}
	}

	for _, name := range f.sourceNodes {
		f.plots[name].Seal()
	}

	for _, plot := range f.plots {
		plot.WaitAsync()
	}

	f.logInlet.EndFarm(f.name, time.Since(startTime).Seconds())

	return nil
}
