package metronome

import "time"

// TickEvent 表示一次节拍触发。
//
// Seq 从 1 开始单调递增。FiredAt 由 time.Now 生成，内部调度依赖其单调时钟
// 分量计算下次触发时刻，因此不受系统墙钟调整影响。
type TickEvent struct {
	Seq     int       // 节拍序号，从 1 开始
	FiredAt time.Time // 实际触发时间
}
