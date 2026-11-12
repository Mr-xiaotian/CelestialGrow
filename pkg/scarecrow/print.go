package scarecrow

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// PrintWatcher 把事件进度打印到标准输出，适合本地调试与演示。
//
// 它自行维护计数，不依赖 MetricsWatcher，因此可以单独挂到任意 Plot 或 Farm。
// 挂到单个 Plot 时输出该节点的进度；挂到 Farm 时输出所有 Plot 的累计进度。
type PrintWatcher struct {
	mu        sync.Mutex
	name      string
	out       io.Writer
	total     int
	succeeded int
	failed    int
	skipped   int
}

// NewPrintWatcher 创建一个打印观察者，name 用作输出前缀。
func NewPrintWatcher(name string) *PrintWatcher {
	return newPrintWatcher(os.Stdout, name)
}

// newPrintWatcher 使用指定输出创建观察者，便于测试捕获输出。
func newPrintWatcher(out io.Writer, name string) *PrintWatcher {
	return &PrintWatcher{name: name, out: out}
}

// OnEvent 根据事件类型打印进度。
func (p *PrintWatcher) OnEvent(event Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch event.(type) {
	case PlotStartEvent:
		p.printf("start total=%d", p.total)

	case PlotEndEvent:
		p.printf(
			"finish total=%d, skipped=%d, succeeded=%d, failed=%d",
			p.total, p.skipped, p.succeeded, p.failed,
		)

	case SeedInputEvent:
		p.total++
		p.printf("total=%d(+1)", p.total)

	case SeedRipenEvent:
		p.succeeded++
		p.printf("succeeded=%d(+1), total=%d", p.succeeded, p.total)

	case SeedWitherEvent:
		p.failed++
		p.printf("failed=%d(+1), total=%d", p.failed, p.total)

	case SeedPruneEvent:
		p.skipped++
		p.printf("skipped=%d(+1), total=%d", p.skipped, p.total)

	case FarmStartEvent, FarmEndEvent, SeedReplantEvent, SealInputEvent, SealMergeEvent:
		// 与 CelestialFlow 的 PrintObserver 一致，只关注节点与任务进度。
	}

	return nil
}

// printf 输出一行带前缀的进度信息。调用方需自行持有锁。
func (p *PrintWatcher) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, "[%s] %s\n", p.name, fmt.Sprintf(format, args...))
}
