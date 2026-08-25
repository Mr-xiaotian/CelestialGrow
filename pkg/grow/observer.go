package grow

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

// ==== Interface ====

// Observer 种子培育进度的观察者接口。
// 通过 NewPlot 的 observers 参数注入，Plot 在启动、进度更新和完成时依次调用。
type Observer interface {
	OnStart(total int)
	OnProgress(completed, total int)
	OnFinish(completed, total int)
}

// ==== ProgressBar ====

// ProgressBar 基于 progressbar/v3 的 Observer 实现，在终端实时显示进度条。
type ProgressBar struct {
	description string
	bar         *progressbar.ProgressBar
	mu          sync.Mutex
}

// NewProgressBar 创建一个带描述文本的进度条。description 显示在进度条前缀位置。
func NewProgressBar(description string) *ProgressBar {
	return &ProgressBar{description: description}
}

// ensureBar 延迟初始化进度条，在首次知道 total 时创建。
func (p *ProgressBar) ensureBar(total int) {
	if total == 0 || p.bar != nil {
		return
	}
	p.bar = progressbar.NewOptions64(
		int64(total),
		progressbar.OptionSetDescription(p.description),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(10),
		progressbar.OptionShowTotalBytes(true),
		progressbar.OptionThrottle(time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionOnCompletion(func() {
			_, _ = fmt.Fprint(os.Stderr, "\n")
		}),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),
	)
}

// OnStart 初始化进度条，设置最大值为 total。
func (p *ProgressBar) OnStart(total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ensureBar(total)
}

// OnProgress 更新进度条的当前值为 completed。
func (p *ProgressBar) OnProgress(completed, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ensureBar(total)
	_ = p.bar.Set(completed)
}

// OnFinish 完成进度条渲染，标记为结束状态。
func (p *ProgressBar) OnFinish(completed, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ensureBar(total)
	_ = p.bar.Set(total)
	_ = p.bar.Finish()
}
