package persist

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
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

// ==== Observer ====

// LogObserver 将观察者事件转换为日志记录并异步写入日志文件。
//
// 事件经 funnel.Spout 异步落盘，观察者只负责翻译与投递。节点结束日志中的
// 果实/杂草/剪枝数量由观察者根据任务结果事件累加，因此不依赖事件携带计数。
// 事件可能来自多个并发协程，计数与投递均需并发安全。
type LogObserver struct {
	mu       sync.Mutex
	counts   map[string]*nodeLogCounts
	spout    *funnel.Spout[LogRecord]
	inlet    *funnel.Inlet[LogRecord]
	minLevel int
}

// nodeLogCounts 记录单个节点已产生的任务结果数量。
type nodeLogCounts struct {
	fruit int
	weed  int
	prune int
}

// NewLogObserver 创建一个日志观察者，level 为最低日志级别（不存在则默认 INFO）。
func NewLogObserver(level string) *LogObserver {
	return newLogObserver(&LogRecordHandler{}, level)
}

// newLogObserver 使用指定处理器创建日志观察者，便于测试注入处理器。
func newLogObserver(handler funnel.RecordHandler[LogRecord], level string) *LogObserver {
	minLevel, ok := levelOrder[level]
	if !ok {
		minLevel = levelOrder["INFO"]
	}

	spout := funnel.NewSpout(handler, 100, time.Second)
	return &LogObserver{
		counts:   make(map[string]*nodeLogCounts),
		spout:    spout,
		inlet:    funnel.NewInlet(spout.GetQueue(), time.Second),
		minLevel: minLevel,
	}
}

// Start 启动日志消费循环并打开日志文件。
func (l *LogObserver) Start() error {
	return l.spout.Start()
}

// Stop 停止日志消费循环并关闭日志文件。
func (l *LogObserver) Stop() error {
	return l.spout.Stop()
}

// OnEvent 将事件翻译为日志记录并投递到消费队列。
func (l *LogObserver) OnEvent(event observer.Event) error {
	switch ev := event.(type) {
	case observer.GraphStartEvent:
		if err := l.log("INFO", fmt.Sprintf("Farm '%s' start. Graph structure:", ev.Farm), ev.StartTime); err != nil {
			return err
		}
		for _, line := range ev.Structure {
			if err := l.log("INFO", line, ev.StartTime); err != nil {
				return err
			}
		}
		return nil

	case observer.GraphEndEvent:
		return l.log("INFO", fmt.Sprintf("Farm '%s' end. Use %.2fs.", ev.Farm, ev.Elapsed.Seconds()), ev.EndTime)

	case observer.NodeStartEvent:
		return l.log("INFO", fmt.Sprintf("Plot '%s' start with %d tenders.", ev.Node, ev.Tenders), ev.StartTime)

	case observer.NodeEndEvent:
		counts := l.takeCounts(ev.Node)
		return l.log(
			"INFO",
			fmt.Sprintf(
				"Plot '%s' end. Use %.2fs. %d ripened, %d withered, %d pruned.",
				ev.Node, ev.Elapsed.Seconds(), counts.fruit, counts.weed, counts.prune,
			),
			ev.EndTime,
		)

	case observer.TaskInputEvent:
		return l.log(
			"DEBUG",
			fmt.Sprintf("In '%s', Seed %s input. [%d*]", ev.Node, ev.SeedRepr, ev.SeedID),
			ev.Time,
		)

	case observer.TaskSuccessEvent:
		l.addResult(ev.Node, func(counts *nodeLogCounts) { counts.fruit++ })
		return l.log(
			"SUCCESS",
			fmt.Sprintf(
				"In '%s', Seed %s ripened. Fruit is %s. Use %.2fs. [%d->%d*]",
				ev.Node, ev.SeedRepr, ev.FruitRepr, ev.Elapsed.Seconds(), ev.SeedID, ev.FruitID,
			),
			ev.Time,
		)

	case observer.TaskFailEvent:
		l.addResult(ev.Node, func(counts *nodeLogCounts) { counts.weed++ })
		return l.log(
			"ERROR",
			fmt.Sprintf(
				"In '%s', Seed %s withered: %v. Use %.2fs. [%d->%d*]",
				ev.Node, ev.SeedRepr, ev.Err, ev.Elapsed.Seconds(), ev.SeedID, ev.WeedID,
			),
			ev.Time,
		)

	case observer.TaskSkipEvent:
		l.addResult(ev.Node, func(counts *nodeLogCounts) { counts.prune++ })
		return l.log(
			"INFO",
			fmt.Sprintf(
				"In '%s', Seed %s pruned. Use %.2fs. [%d->%d*]",
				ev.Node, ev.SeedRepr, ev.Elapsed.Seconds(), ev.SeedID, ev.PruneID,
			),
			ev.Time,
		)

	case observer.TaskRetryEvent:
		return l.log(
			"WARNING",
			fmt.Sprintf(
				"In '%s', Seed %s attempt %d withered: %v. Replanting. [%d*]",
				ev.Node, ev.SeedRepr, ev.Attempt, ev.Err, ev.SeedID,
			),
			ev.Time,
		)

	case observer.TerminationInputEvent, observer.TerminationMergeEvent:
		return nil
	}

	return nil
}

// log 发送一条指定级别的日志。低于 minLevel 的日志会被静默丢弃。
func (l *LogObserver) log(level string, message string, at time.Time) error {
	if levelOrder[level] < l.minLevel {
		return nil
	}
	return l.inlet.Send(LogRecord{
		FormatTime: at.Format("2006-01-02 15:04:05"),
		Level:      level,
		Message:    message,
	})
}

// addResult 累加节点的任务结果计数。
func (l *LogObserver) addResult(node string, apply func(*nodeLogCounts)) {
	l.mu.Lock()
	defer l.mu.Unlock()

	counts, ok := l.counts[node]
	if !ok {
		counts = &nodeLogCounts{}
		l.counts[node] = counts
	}
	apply(counts)
}

// takeCounts 取出并清除节点的结果计数，供节点结束日志使用。
func (l *LogObserver) takeCounts(node string) nodeLogCounts {
	l.mu.Lock()
	defer l.mu.Unlock()

	counts, ok := l.counts[node]
	if !ok {
		return nodeLogCounts{}
	}
	delete(l.counts, node)
	return *counts
}
