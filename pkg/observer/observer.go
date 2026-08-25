package observer

// ==== Interface ====

// Observer 种子培育进度的观察者接口。
// 通过 NewPlot 的 observers 参数注入，Plot 在启动、进度更新和完成时依次调用。
type Observer interface {
	OnStart(total int)
	OnProgress(completed, total int)
	OnFinish(completed, total int)
}
