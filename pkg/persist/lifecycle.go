package persist

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/scarecrow"
)

const (
	lifecycleSeed   = "seed"
	lifecycleRipen  = "ripen"
	lifecycleWither = "wither"
	lifecyclePrune  = "prune"
)

// LifecycleRecord 是生命周期记录的封闭类型联合，仅由 SeedRecord、RipenRecord、
// WitherRecord、PruneRecord 实现。具体类型决定写入 events 表的 event_type，
// 不再依赖运行期 Kind 字段。
type LifecycleRecord interface {
	lifecycleRecord()
}

// SeedRecord 表示一次种子输入。
type SeedRecord struct {
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	SeedJSON       string
	TS             float64
}

// lifecycleRecord 标记 SeedRecord 实现 LifecycleRecord。
func (SeedRecord) lifecycleRecord() {}

// RipenRecord 表示一次种子成熟。
type RipenRecord struct {
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	FruitJSON      string
	TS             float64
}

// lifecycleRecord 标记 RipenRecord 实现 LifecycleRecord。
func (RipenRecord) lifecycleRecord() {}

// WitherRecord 表示一次种子枯萎。
type WitherRecord struct {
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	WitherType     string
	WitherMessage  string
	TS             float64
}

// lifecycleRecord 标记 WitherRecord 实现 LifecycleRecord。
func (WitherRecord) lifecycleRecord() {}

// PruneRecord 表示一次种子剪枝。
type PruneRecord struct {
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	TS             float64
}

// lifecycleRecord 标记 PruneRecord 实现 LifecycleRecord。
func (PruneRecord) lifecycleRecord() {}

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

// HandleRecord 将一条生命周期记录写入 events 表，并推进对应的 status 快照。
// 记录类型直接决定 event_type，无需再校验 Kind 与字段是否匹配。
func (l *LifecycleRecordHandler) HandleRecord(record LifecycleRecord) error {
	if l.sqliteDB == nil {
		return fmt.Errorf("生命周期 sqlite 未初始化")
	}

	insertEvent := func(eventID int, eventType string, plot string, ts float64, parentIDs []int) error {
		return InsertLifecycleEvent(l.sqliteDB, eventID, eventType, plot, ts, parentIDs)
	}

	switch rec := record.(type) {
	case SeedRecord:
		if err := insertEvent(rec.CurrentEventID, lifecycleSeed, rec.PlotName, rec.TS, rec.ParentIDs); err != nil {
			return err
		}
		return UpsertLifecycleStatusSeed(l.sqliteDB,
			rec.InputEventID,
			rec.SeedJSON,
			rec.PlotName,
			rec.TS,
		)
	case RipenRecord:
		if err := insertEvent(rec.CurrentEventID, lifecycleRipen, rec.PlotName, rec.TS, rec.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusRipen(l.sqliteDB, rec.InputEventID, rec.CurrentEventID, rec.FruitJSON, rec.TS)
	case WitherRecord:
		if err := insertEvent(rec.CurrentEventID, lifecycleWither, rec.PlotName, rec.TS, rec.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusWither(
			l.sqliteDB,
			rec.InputEventID,
			rec.CurrentEventID,
			rec.WitherType,
			rec.WitherMessage,
			rec.TS,
		)
	case PruneRecord:
		if err := insertEvent(rec.CurrentEventID, lifecyclePrune, rec.PlotName, rec.TS, rec.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusPrune(l.sqliteDB, rec.InputEventID, rec.CurrentEventID, rec.TS)
	default:
		return fmt.Errorf("unsupported lifecycle record: %T", record)
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

// ==== Watcher ====

// LifecycleWatcher 将观察者事件转换为生命周期记录并异步写入 sqlite。
//
// 种子输入、成熟、枯萎与剪枝事件分别映射为 SeedRecord / RipenRecord /
// WitherRecord / PruneRecord；事件携带的事件 ID 与父事件 ID 直接用于重建生命周期图。
type LifecycleWatcher struct {
	spout *funnel.Spout[LifecycleRecord]
	inlet *funnel.Inlet[LifecycleRecord]
}

// NewLifecycleWatcher 创建一个生命周期观察者。
func NewLifecycleWatcher() *LifecycleWatcher {
	return newLifecycleWatcher(&LifecycleRecordHandler{})
}

// newLifecycleWatcher 使用指定处理器创建观察者，便于测试注入处理器。
func newLifecycleWatcher(handler funnel.RecordHandler[LifecycleRecord]) *LifecycleWatcher {
	spout := funnel.NewSpout(handler, 100, time.Second)
	return &LifecycleWatcher{
		spout: spout,
		inlet: funnel.NewInlet(spout.GetQueue(), time.Second),
	}
}

// Start 启动生命周期消费循环并打开 sqlite 数据库。
func (l *LifecycleWatcher) Start() error {
	return l.spout.Start()
}

// Stop 停止消费循环并关闭 sqlite 数据库。
func (l *LifecycleWatcher) Stop() error {
	return l.spout.Stop()
}

// DBPath 返回生命周期数据库路径；消费循环尚未启动时返回空字符串。
func (l *LifecycleWatcher) DBPath() string {
	handler, ok := l.spout.Handler().(*LifecycleRecordHandler)
	if !ok {
		return ""
	}
	return handler.SQLitePath
}

// OnEvent 将事件翻译为生命周期记录并投递到消费队列。
func (l *LifecycleWatcher) OnEvent(event scarecrow.Event) error {
	switch ev := event.(type) {
	case scarecrow.SeedInputEvent:
		return l.inlet.Send(SeedRecord{
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.SeedID,
			ParentIDs:      ev.Parents,
			PlotName:       ev.Plot,
			SeedJSON:       toLifecycleJSON(ev.Seed),
			TS:             unixSeconds(ev.Time),
		})

	case scarecrow.SeedRipenEvent:
		return l.inlet.Send(RipenRecord{
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.FruitID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			FruitJSON:      toLifecycleJSON(ev.Fruit),
			TS:             unixSeconds(ev.Time),
		})

	case scarecrow.SeedWitherEvent:
		return l.inlet.Send(WitherRecord{
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.WeedID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			WitherType:     fmt.Sprintf("%T", ev.Err),
			WitherMessage:  fmt.Sprintf("%v", ev.Err),
			TS:             unixSeconds(ev.Time),
		})

	case scarecrow.SeedPruneEvent:
		return l.inlet.Send(PruneRecord{
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.PruneID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			TS:             unixSeconds(ev.Time),
		})

	case scarecrow.FarmStartEvent, scarecrow.FarmEndEvent,
		scarecrow.PlotStartEvent, scarecrow.PlotEndEvent,
		scarecrow.SeedReplantEvent,
		scarecrow.SealInputEvent, scarecrow.SealMergeEvent:
		return nil
	}

	return nil
}

// unixSeconds 将时间转换为 Unix 秒（保留毫秒精度）。
func unixSeconds(t time.Time) float64 {
	return float64(t.UnixMilli()) / 1000
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
