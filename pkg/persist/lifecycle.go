package persist

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
)

const (
	lifecycleSeed   = "seed"
	lifecycleRipen  = "ripen"
	lifecycleWither = "wither"
)

// LifecycleRecord 表示一条生命周期持久化操作。
type LifecycleRecord struct {
	Kind           string
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	TaskJSON       string
	ResultJSON     string
	ErrorType      string
	ErrorMessage   string
	TS             float64
}

// LifecycleRecordHandler 消费生命周期操作并将其写入 sqlite。
type LifecycleRecordHandler struct {
	SQLitePath string
	sqliteDB   *sql.DB
}

// BeforeStart 创建 lifecycle 目录并打开 sqlite 数据库。
func (l *LifecycleRecordHandler) BeforeStart() error {
	today := time.Now().Format("2006-01-02")
	now := time.Now().Format("15-04-05.000")
	dir := filepath.Join("lifecycles", today)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建生命周期目录失败: %w", err)
	}

	l.SQLitePath = filepath.Join(dir, fmt.Sprintf("grow_lifecycle(%s).sqlite3", now))
	db, err := OpenLifecycleSQLite(l.SQLitePath)
	if err != nil {
		return fmt.Errorf("打开生命周期 sqlite 失败: %w", err)
	}
	l.sqliteDB = db
	return nil
}

// HandleRecord 执行一条 seed 生命周期操作。
func (l *LifecycleRecordHandler) HandleRecord(record LifecycleRecord) error {
	if l.sqliteDB == nil {
		return fmt.Errorf("生命周期 sqlite 未初始化")
	}

	switch record.Kind {
	case lifecycleSeed:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return UpsertLifecycleStatusSeed(l.sqliteDB,
			record.InputEventID,
			record.TaskJSON,
			record.PlotName,
			record.TS,
		)
	case lifecycleRipen:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusRipen(l.sqliteDB, record.InputEventID, record.CurrentEventID, record.ResultJSON, record.TS)
	case lifecycleWither:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusWither(
			l.sqliteDB,
			record.InputEventID,
			record.CurrentEventID,
			record.ErrorType,
			record.ErrorMessage,
			record.TS,
		)
	default:
		return fmt.Errorf("unsupported lifecycle operation: %s", record.Kind)
	}
}

// AfterStop 关闭 sqlite 数据库句柄。
func (l *LifecycleRecordHandler) AfterStop() error {
	if l.sqliteDB == nil {
		return nil
	}
	err := l.sqliteDB.Close()
	l.sqliteDB = nil
	if err != nil {
		return fmt.Errorf("关闭生命周期 sqlite 失败: %w", err)
	}
	return nil
}

// LoadStatuses 读取指定 plot 的全部任务状态快照。
func (l *LifecycleRecordHandler) LoadStatuses(plotName string) ([]LifecycleStatusRecord, error) {
	if l.sqliteDB != nil {
		return LoadLifecycleStatuses(l.sqliteDB, plotName)
	}

	if l.SQLitePath == "" {
		return nil, fmt.Errorf("生命周期 sqlite 未初始化")
	}

	db, err := OpenLifecycleSQLite(l.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("重新打开生命周期 sqlite 失败: %w", err)
	}
	defer db.Close()

	return LoadLifecycleStatuses(db, plotName)
}

// LifecycleInlet 生产生命周期记录。
type LifecycleInlet struct {
	funnel.Inlet[LifecycleRecord]
}

// NewLifecycleInlet 创建 LifecycleInlet。
func NewLifecycleInlet(ch chan<- LifecycleRecord, timeout time.Duration) *LifecycleInlet {
	return &LifecycleInlet{
		Inlet: *funnel.NewInlet(ch, timeout),
	}
}

// SeedIn 记录一条输入事件和对应的 pending 状态。
func (l *LifecycleInlet) SeedIn(plot string, eventID int, parentIDs []int, task any) {
	now := time.Now().UnixMilli()
	l.Send(LifecycleRecord{
		Kind:           lifecycleSeed,
		CurrentEventID: eventID,
		InputEventID:   eventID,
		ParentIDs:      parentIDs,
		PlotName:       plot,
		TaskJSON:       toLifecycleJSON(task),
		TS:             float64(now) / 1000,
	})
}

// SeedRipen 记录成功事件并将状态晋升为 ripen。
func (l *LifecycleInlet) SeedRipen(plot string, inputEventID int, parentEventID int, ripenEventID int, result any) {
	now := time.Now().UnixMilli()
	l.Send(LifecycleRecord{
		Kind:           lifecycleRipen,
		CurrentEventID: ripenEventID,
		InputEventID:   inputEventID,
		ParentIDs:      []int{parentEventID},
		ResultJSON:     toLifecycleJSON(result),
		PlotName:       plot,
		TS:             float64(now) / 1000,
	})
}

// SeedWither 记录失败事件并将状态晋升为 wither。
func (l *LifecycleInlet) SeedWither(plot string, inputEventID int, parentEventID int, witherEventID int, err error) {
	now := time.Now().UnixMilli()
	l.Send(LifecycleRecord{
		Kind:           lifecycleWither,
		CurrentEventID: witherEventID,
		InputEventID:   inputEventID,
		PlotName:       plot,
		ParentIDs:      []int{parentEventID},
		ErrorType:      fmt.Sprintf("%T", err),
		ErrorMessage:   fmt.Sprintf("%v", err),
		TS:             float64(now) / 1000,
	})
}

// toLifecycleJSON 将任意值序列化为可写入 sqlite 的 JSON 文本。
func toLifecycleJSON(value any) string {
	data, err := json.Marshal(value)
	if err == nil {
		return string(data)
	}

	lifecycle, lifecycleErr := json.Marshal(fmt.Sprintf("%+v", value))
	if lifecycleErr == nil {
		return string(lifecycle)
	}

	return `"marshal_error"`
}
