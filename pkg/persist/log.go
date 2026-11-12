package persist

import (
	"fmt"
	"os"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

// ==== Constants ====

// levelOrder 日志级别优先级映射表，数值越小优先级越低。
var levelOrder = map[string]int{
	"TRACE":    0,
	"DEBUG":    1,
	"SUCCESS":  2,
	"INFO":     3,
	"WARNING":  4,
	"ERROR":    5,
	"CRITICAL": 6,
}

// ==== Record ====

// LogRecord 单条日志记录。
type LogRecord struct {
	FormatTime string
	Level      string
	Message    string
}

// ==== Record Handler (Spout) ====

// LogRecordHandler 日志记录的消费端处理器。
// 实现 funnel.RecordHandler[LogRecord] 接口，将日志格式化后写入文件。
type LogRecordHandler struct {
	LogPath string
	logFile *os.File
}

// BeforeStart 创建 logs/ 目录并打开日志文件（按日期命名，追加模式）。
func (l *LogRecordHandler) BeforeStart() error {
	var err error

	today := time.Now().Format("2006-01-02")
	l.LogPath = fmt.Sprintf("logs/grow_log(%s).log", today)
	if err = os.MkdirAll("logs", 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	l.logFile, err = os.OpenFile(l.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	return nil
}

// HandleRecord 将日志记录格式化为一行文本并追加写入文件。
func (l *LogRecordHandler) HandleRecord(record LogRecord) error {
	line := fmt.Sprintf("%s %s %s\n", record.FormatTime, record.Level, record.Message)

	_, err := l.logFile.WriteString(line)
	if err != nil {
		return fmt.Errorf("写入日志文件失败: %w", err)
	}

	return nil
}

// AfterStop 关闭日志文件句柄。
func (l *LogRecordHandler) AfterStop() error {
	err := l.logFile.Close()
	if err != nil {
		return fmt.Errorf("关闭日志文件失败: %w", err)
	}

	return nil
}

// ==== Watcher ====

// LogWatcher 将观察者事件转换为日志记录并异步写入日志文件。
//
// 事件经 funnel.Spout 异步落盘，观察者只负责翻译与投递。Plot 结束日志中的
// 果实/杂草/剪枝数量直接读取 metrics 快照，不再自行计数。
// 事件可能来自多个并发协程，观察者本身无状态，投递依赖 funnel.Inlet 的并发安全。
type LogWatcher struct {
	spout    *funnel.Spout[LogRecord]
	inlet    *funnel.Inlet[LogRecord]
	metrics  *scarecrow.MetricsWatcher
	minLevel int
}

// NewLogWatcher 创建一个日志观察者。
// metrics 提供 Plot 结束时的结果计数；level 为最低日志级别（不存在则默认 INFO）。
func NewLogWatcher(metrics *scarecrow.MetricsWatcher, level string) *LogWatcher {
	return newLogWatcher(&LogRecordHandler{}, metrics, level)
}

// newLogWatcher 使用指定处理器创建日志观察者，便于测试注入处理器。
func newLogWatcher(handler funnel.RecordHandler[LogRecord], metrics *scarecrow.MetricsWatcher, level string) *LogWatcher {
	minLevel, ok := levelOrder[level]
	if !ok {
		minLevel = levelOrder["INFO"]
	}

	spout := funnel.NewSpout(handler, 100, time.Second)
	return &LogWatcher{
		spout:    spout,
		inlet:    funnel.NewInlet(spout.GetQueue(), time.Second),
		metrics:  metrics,
		minLevel: minLevel,
	}
}

// Start 启动日志消费循环并打开日志文件。
func (l *LogWatcher) Start() error {
	return l.spout.Start()
}

// Stop 停止日志消费循环并关闭日志文件。
func (l *LogWatcher) Stop() error {
	return l.spout.Stop()
}

// OnEvent 将事件翻译为日志记录并投递到消费队列。
func (l *LogWatcher) OnEvent(event scarecrow.Event) error {
	switch ev := event.(type) {
	case scarecrow.FarmStartEvent:
		if err := l.log("INFO", fmt.Sprintf("Farm '%s' start. Structure:", ev.Farm), ev.StartTime); err != nil {
			return err
		}
		for _, line := range ev.Structure {
			if err := l.log("INFO", line, ev.StartTime); err != nil {
				return err
			}
		}
		return nil

	case scarecrow.FarmEndEvent:
		return l.log("INFO", fmt.Sprintf("Farm '%s' end. Use %.2fs.", ev.Farm, ev.Elapsed.Seconds()), ev.EndTime)

	case scarecrow.PlotStartEvent:
		return l.log("INFO", fmt.Sprintf("Plot '%s' start with %d tenders.", ev.Plot, ev.Tenders), ev.StartTime)

	case scarecrow.PlotEndEvent:
		var counts scarecrow.PlotMetrics
		if l.metrics != nil {
			counts, _ = l.metrics.GetPlot(ev.Plot)
		}
		return l.log(
			"INFO",
			fmt.Sprintf(
				"Plot '%s' end. Use %.2fs. %d ripened, %d withered, %d pruned.",
				ev.Plot, ev.Elapsed.Seconds(), counts.Succeeded, counts.Failed, counts.Skipped,
			),
			ev.EndTime,
		)

	case scarecrow.SeedInputEvent:
		return l.log(
			"DEBUG",
			fmt.Sprintf("In '%s', Seed %s input. [%d*]", ev.Plot, ev.SeedRepr, ev.SeedID),
			ev.Time,
		)

	case scarecrow.SeedRipenEvent:
		return l.log(
			"SUCCESS",
			fmt.Sprintf(
				"In '%s', Seed %s ripened. Fruit is %s. Use %.2fs. [%d->%d*]",
				ev.Plot, ev.SeedRepr, ev.FruitRepr, ev.Elapsed.Seconds(), ev.SeedID, ev.FruitID,
			),
			ev.Time,
		)

	case scarecrow.SeedWitherEvent:
		return l.log(
			"ERROR",
			fmt.Sprintf(
				"In '%s', Seed %s withered: %v. Use %.2fs. [%d->%d*]",
				ev.Plot, ev.SeedRepr, ev.Err, ev.Elapsed.Seconds(), ev.SeedID, ev.WeedID,
			),
			ev.Time,
		)

	case scarecrow.SeedPruneEvent:
		return l.log(
			"INFO",
			fmt.Sprintf(
				"In '%s', Seed %s pruned. Use %.2fs. [%d->%d*]",
				ev.Plot, ev.SeedRepr, ev.Elapsed.Seconds(), ev.SeedID, ev.PruneID,
			),
			ev.Time,
		)

	case scarecrow.SeedReplantEvent:
		return l.log(
			"WARNING",
			fmt.Sprintf(
				"In '%s', Seed %s attempt %d withered: %v. Replanting. [%d*]",
				ev.Plot, ev.SeedRepr, ev.Attempt, ev.Err, ev.SeedID,
			),
			ev.Time,
		)

	case scarecrow.SealInputEvent, scarecrow.SealMergeEvent:
		return nil
	}

	return nil
}

// log 发送一条指定级别的日志。低于 minLevel 的日志会被静默丢弃。
func (l *LogWatcher) log(level string, message string, at time.Time) error {
	if levelOrder[level] < l.minLevel {
		return nil
	}
	return l.inlet.Send(LogRecord{
		FormatTime: at.Format("2006-01-02 15:04:05"),
		Level:      level,
		Message:    message,
	})
}
