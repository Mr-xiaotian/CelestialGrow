package observer

import "time"

// ==== Interface ====

// Event 是 Observer 收到的事件。
//
// 事件集合由本包封闭定义，包外无法新增事件类型，因此 Hub 的 type switch
// 可以覆盖全部事件。事件以值传递，其内部的切片与映射由发送方持有，
// 观察者只能读取，不得修改。
type Event interface {
	isEvent()
}

// ==== Farm ====

// FarmStartEvent Farm 启动事件。
type FarmStartEvent struct {
	Farm        string              // Farm 名称
	IsDAG       bool                // 是否为有向无环图
	Plots       []string            // Plot 名称列表
	Edges       map[string][]string // 出边邻接表
	SourcePlots []string            // 源 Plot 名称列表
	Structure   []string            // 预先渲染的结构行
	StartTime   time.Time           // 启动时间
}

func (FarmStartEvent) isEvent() {}

// FarmEndEvent Farm 结束事件。
type FarmEndEvent struct {
	Farm    string        // Farm 名称
	EndTime time.Time     // 结束时间
	Elapsed time.Duration // 运行耗时
}

func (FarmEndEvent) isEvent() {}

// ==== Plot ====

// PlotStartEvent Plot 启动事件。
type PlotStartEvent struct {
	Plot      string    // Plot 名称
	Tenders   int       // 并发处理协程数
	StartTime time.Time // 启动时间
}

func (PlotStartEvent) isEvent() {}

// PlotEndEvent Plot 结束事件。
type PlotEndEvent struct {
	Plot    string        // Plot 名称
	EndTime time.Time     // 结束时间
	Elapsed time.Duration // 运行耗时
}

func (PlotEndEvent) isEvent() {}

// ==== Seed ====

// SeedInputEvent 种子输入事件。
// From 为空表示由外部直接注入，非空表示由上游 Plot 投递。
type SeedInputEvent struct {
	Plot     string    // 接收种子的 Plot 名称
	From     string    // 上游来源 Plot 名称
	Seed     any       // 原始种子数据
	SeedRepr string    // 种子的可读表示
	SeedID   int       // 当前输入事件 ID
	Parents  []int     // 父事件 ID；外部注入时为空
	Time     time.Time // 输入时间
}

func (SeedInputEvent) isEvent() {}

// SeedRipenEvent 种子成熟（培育成功）事件。
type SeedRipenEvent struct {
	Plot      string        // Plot 名称
	Seed      any           // 原始种子数据
	SeedRepr  string        // 种子的可读表示
	Fruit     any           // 培育结果
	FruitRepr string        // 结果的可读表示
	Elapsed   time.Duration // 培育耗时
	SeedID    int           // 种子输入事件 ID
	FruitID   int           // 成熟事件 ID
	Time      time.Time     // 完成时间
}

func (SeedRipenEvent) isEvent() {}

// SeedWitherEvent 种子枯萎（培育失败）事件。
type SeedWitherEvent struct {
	Plot     string        // Plot 名称
	Seed     any           // 原始种子数据
	SeedRepr string        // 种子的可读表示
	Err      error         // 导致失败的错误
	Elapsed  time.Duration // 培育耗时
	SeedID   int           // 种子输入事件 ID
	WeedID   int           // 枯萎事件 ID
	Time     time.Time     // 完成时间
}

func (SeedWitherEvent) isEvent() {}

// SeedPruneEvent 种子剪枝（跳过培育）事件。
type SeedPruneEvent struct {
	Plot     string        // Plot 名称
	Seed     any           // 原始种子数据
	SeedRepr string        // 种子的可读表示
	Elapsed  time.Duration // 判定耗时
	SeedID   int           // 种子输入事件 ID
	PruneID  int           // 剪枝事件 ID
	Time     time.Time     // 完成时间
}

func (SeedPruneEvent) isEvent() {}

// SeedReplantEvent 种子补种（重试）事件。
type SeedReplantEvent struct {
	Plot     string    // Plot 名称
	Seed     any       // 原始种子数据
	SeedRepr string    // 种子的可读表示
	Err      error     // 导致重试的错误
	Attempt  int       // 已重试次数，从 1 开始
	SeedID   int       // 种子输入事件 ID
	Time     time.Time // 重试时间
}

func (SeedReplantEvent) isEvent() {}

// ==== Seal ====

// SealInputEvent 外部终止信号输入事件。
type SealInputEvent struct {
	Plot   string    // 接收终止信号的 Plot 名称
	SealID int       // 终止信号事件 ID
	Time   time.Time // 收到时间
}

func (SealInputEvent) isEvent() {}

// SealMergeEvent 终止信号合并事件。
type SealMergeEvent struct {
	Plot      string    // 执行合并的 Plot 名称
	ParentIDs []int     // 参与合并的终止信号事件 ID
	SealID    int       // 合并后的终止信号事件 ID
	Time      time.Time // 合并时间
}

func (SealMergeEvent) isEvent() {}
