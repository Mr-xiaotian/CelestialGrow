package runtime

// ==== Constants ====

const (
	SignalNone = iota // 正常数据
	SignalSeal        // 终止信号，通知下游不再有新数据
)

// ==== Types ====

// Payload 管道阶段的统一数据载体。
// 同时承载正常数据和控制信号，使数据流和控制流共用同一通道。
// Signal 为 SignalNone 时为正常数据，为 SignalSeal 时为终止信号。
type Payload[V any] struct {
	// Signal与Seed通用
	Signal  int
	EventID int

	// Signal使用
	Source string

	// Seed使用
	Value V
}

// Karma 种子与果实的配对，记录一颗种子培育后的完整结果。
type Karma[S any, F any] struct {
	Seed  S
	Fruit F
}
