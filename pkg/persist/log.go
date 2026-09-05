package persist

import (
	"fmt"
	"os"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
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

// ==== Inlet ====

// LogInlet 日志生产端。内嵌 funnel.Inlet 并提供级别过滤，
// 低于 minLevel 的日志不会被发送到通道。
type LogInlet struct {
	funnel.Inlet[LogRecord]
	minLevel int
}

// NewLogInlet 创建 LogInlet。
// level 指定最低日志级别（不存在则默认 INFO），timeout 为发送超时。
func NewLogInlet(ch chan<- LogRecord, timeout time.Duration, level string) *LogInlet {
	minLevel, ok := levelOrder[level]
	if !ok {
		minLevel = levelOrder["INFO"]
	}
	return &LogInlet{
		Inlet:    *funnel.NewInlet(ch, timeout),
		minLevel: minLevel,
	}
}

// ==== Log Methods ====

// log 发送一条指定级别的日志。低于 minLevel 的日志会被静默丢弃。
func (l *LogInlet) log(level string, message string) {
	if levelOrder[level] < l.minLevel {
		return
	}
	l.Send(LogRecord{
		FormatTime: time.Now().Format("2006-01-02 15:04:05"),
		Level:      level,
		Message:    message,
	})
}

// StartFarm 记录 Farm 启动。
func (l *LogInlet) StartFarm(farmName string, structureList []string) {
	l.log("INFO", fmt.Sprintf("Farm '%s' start. Graph structure:", farmName))
	for _, s := range structureList {
		l.log("INFO", s)
	}
}

// EndFarm 记录 Farm 结束，包含总耗时。
func (l *LogInlet) EndFarm(farmName string, useTime float64) {
	l.log("INFO", fmt.Sprintf("Farm '%s' end. Use %.2fs.", farmName, useTime))
}

// StartPlot 记录 Plot 启动，包含 tend 数量。
func (l *LogInlet) StartPlot(plotName string, numTends int) {
	l.log("INFO", fmt.Sprintf("Plot '%s' start by %d tends.", plotName, numTends))
}

// EndPlot 记录 Plot 结束，包含耗时、成功数和失败数。
func (l *LogInlet) EndPlot(plotName string, useTime float64, fruitNum, weedNum int) {
	l.log("INFO", fmt.Sprintf("Plot '%s' end. Use %.2fs. %d ripened, %d withered.", plotName, useTime, fruitNum, weedNum))
}

// SeedRipen 记录种子成熟（培育成功），包含种子和果实的字符串表示及耗时。
func (l *LogInlet) SeedRipen(plotName string, seedRepr string, fruitRepr string, useTime float64, seedID int, fruitID int) {
	l.log("SUCCESS", fmt.Sprintf("In '%s', Seed %s ripened. Fruit is %s. Use %.2fs. [%d->%d*]", plotName, seedRepr, fruitRepr, useTime, seedID, fruitID))
}

// SeedWither 记录种子枯萎（培育失败），包含错误信息和耗时。
func (l *LogInlet) SeedWither(plotName string, seedRepr string, err error, useTime float64, seedID int, weedID int) {
	l.log("ERROR", fmt.Sprintf("In '%s', Seed %s withered: %v. Use %.2fs. [%d->%d*]", plotName, seedRepr, err, useTime, seedID, weedID))
}

// SeedReplant 记录种子重新种植（重试），包含当前尝试次数和错误信息。
func (l *LogInlet) SeedReplant(plotName string, seedRepr string, attempt int, err error) {
	l.log("WARNING", fmt.Sprintf("In '%s', Seed %s attempt %d withered: %v. Replanting...", plotName, seedRepr, attempt, err))
}
