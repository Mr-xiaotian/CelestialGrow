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

// ==== Graph ====

// GraphStartEvent 任务图启动事件。
type GraphStartEvent struct {
	Farm      string              // 任务图名称
	IsDAG     bool                // 是否为有向无环图
	Nodes     []string            // 节点名称列表
	Edges     map[string][]string // 出边邻接表
	Sources   []string            // 源节点名称列表
	Structure []string            // 预先渲染的结构行
	StartTime time.Time           // 启动时间
}

func (GraphStartEvent) isEvent() {}

// GraphEndEvent 任务图结束事件。
type GraphEndEvent struct {
	Farm    string        // 任务图名称
	EndTime time.Time     // 结束时间
	Elapsed time.Duration // 运行耗时
}

func (GraphEndEvent) isEvent() {}

// ==== Node ====

// NodeStartEvent 节点启动事件。
type NodeStartEvent struct {
	Node      string    // 节点名称
	Tenders   int       // 并发处理协程数
	StartTime time.Time // 启动时间
}

func (NodeStartEvent) isEvent() {}

// NodeEndEvent 节点结束事件。
type NodeEndEvent struct {
	Node    string        // 节点名称
	EndTime time.Time     // 结束时间
	Elapsed time.Duration // 运行耗时
}

func (NodeEndEvent) isEvent() {}

// ==== Task ====

// TaskInputEvent 任务输入事件。
// From 为空表示由外部直接注入，非空表示由上游节点投递。
type TaskInputEvent struct {
	Node     string    // 接收任务的节点名称
	From     string    // 上游来源节点名称
	Seed     any       // 原始任务数据
	SeedRepr string    // 任务的可读表示
	SeedID   int       // 当前输入事件 ID
	Parents  []int     // 父事件 ID；外部注入时为空
	Time     time.Time // 输入时间
}

func (TaskInputEvent) isEvent() {}

// TaskSuccessEvent 任务成功事件。
type TaskSuccessEvent struct {
	Node      string        // 节点名称
	Seed      any           // 原始任务数据
	SeedRepr  string        // 任务的可读表示
	Fruit     any           // 任务执行结果
	FruitRepr string        // 结果的可读表示
	Elapsed   time.Duration // 执行耗时
	SeedID    int           // 任务输入事件 ID
	FruitID   int           // 成功事件 ID
	Time      time.Time     // 完成时间
}

func (TaskSuccessEvent) isEvent() {}

// TaskFailEvent 任务失败事件。
type TaskFailEvent struct {
	Node     string        // 节点名称
	Seed     any           // 原始任务数据
	SeedRepr string        // 任务的可读表示
	Err      error         // 导致失败的错误
	Elapsed  time.Duration // 执行耗时
	SeedID   int           // 任务输入事件 ID
	WeedID   int           // 失败事件 ID
	Time     time.Time     // 完成时间
}

func (TaskFailEvent) isEvent() {}

// TaskSkipEvent 任务跳过事件（被剪枝，未进入培育）。
type TaskSkipEvent struct {
	Node     string        // 节点名称
	Seed     any           // 原始任务数据
	SeedRepr string        // 任务的可读表示
	Elapsed  time.Duration // 判定耗时
	SeedID   int           // 任务输入事件 ID
	PruneID  int           // 跳过事件 ID
	Time     time.Time     // 完成时间
}

func (TaskSkipEvent) isEvent() {}

// TaskRetryEvent 任务重试事件。
type TaskRetryEvent struct {
	Node     string    // 节点名称
	Seed     any       // 原始任务数据
	SeedRepr string    // 任务的可读表示
	Err      error     // 导致重试的错误
	Attempt  int       // 已重试次数，从 1 开始
	SeedID   int       // 任务输入事件 ID
	Time     time.Time // 重试时间
}

func (TaskRetryEvent) isEvent() {}

// ==== Termination ====

// TerminationInputEvent 终止信号输入事件。
type TerminationInputEvent struct {
	Node   string    // 接收终止信号的节点名称
	SealID int       // 终止信号事件 ID
	Time   time.Time // 收到时间
}

func (TerminationInputEvent) isEvent() {}

// TerminationMergeEvent 终止信号合并事件。
type TerminationMergeEvent struct {
	Node      string    // 执行合并的节点名称
	ParentIDs []int     // 参与合并的终止信号事件 ID
	SealID    int       // 合并后的终止信号事件 ID
	Time      time.Time // 合并时间
}

func (TerminationMergeEvent) isEvent() {}
