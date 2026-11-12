package persist

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mr-xiaotian/CelestialGrow/pkg/funnel"
	"github.com/Mr-xiaotian/CelestialGrow/pkg/observer"
)

const (
	lifecycleSeed   = "seed"
	lifecycleRipen  = "ripen"
	lifecycleWither = "wither"
	lifecyclePrune  = "prune"
)

// LifecycleRecord 表示一条生命周期持久化操作。
type LifecycleRecord struct {
	Kind           string
	InputEventID   int
	CurrentEventID int
	ParentIDs      []int
	PlotName       string
	SeedJSON       string
	FruitJSON      string
	WitherType     string
	WitherMessage  string
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
			record.SeedJSON,
			record.PlotName,
			record.TS,
		)
	case lifecycleRipen:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusRipen(l.sqliteDB, record.InputEventID, record.CurrentEventID, record.FruitJSON, record.TS)
	case lifecycleWither:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusWither(
			l.sqliteDB,
			record.InputEventID,
			record.CurrentEventID,
			record.WitherType,
			record.WitherMessage,
			record.TS,
		)
	case lifecyclePrune:
		if err := InsertLifecycleEvent(l.sqliteDB, record.CurrentEventID, record.Kind, record.PlotName, record.TS, record.ParentIDs); err != nil {
			return err
		}
		return PromoteLifecycleStatusPrune(l.sqliteDB, record.InputEventID, record.CurrentEventID, record.TS)
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

// ==== Observer ====

// LifecycleObserver 将观察者事件转换为生命周期记录并异步写入 sqlite。
//
// 种子输入、成熟、枯萎与剪枝事件分别映射为 seed / ripen / wither / prune 记录；
// 事件携带的事件 ID 与父事件 ID 直接用于重建生命周期图。
type LifecycleObserver struct {
	spout *funnel.Spout[LifecycleRecord]
	inlet *funnel.Inlet[LifecycleRecord]
}

// NewLifecycleObserver 创建一个生命周期观察者。
func NewLifecycleObserver() *LifecycleObserver {
	return newLifecycleObserver(&LifecycleRecordHandler{})
}

// newLifecycleObserver 使用指定处理器创建观察者，便于测试注入处理器。
func newLifecycleObserver(handler funnel.RecordHandler[LifecycleRecord]) *LifecycleObserver {
	spout := funnel.NewSpout(handler, 100, time.Second)
	return &LifecycleObserver{
		spout: spout,
		inlet: funnel.NewInlet(spout.GetQueue(), time.Second),
	}
}

// Start 启动生命周期消费循环并打开 sqlite 数据库。
func (l *LifecycleObserver) Start() error {
	return l.spout.Start()
}

// Stop 停止消费循环并关闭 sqlite 数据库。
func (l *LifecycleObserver) Stop() error {
	return l.spout.Stop()
}

// DBPath 返回生命周期数据库路径；消费循环尚未启动时返回空字符串。
func (l *LifecycleObserver) DBPath() string {
	handler, ok := l.spout.Handler().(*LifecycleRecordHandler)
	if !ok {
		return ""
	}
	return handler.SQLitePath
}

// OnEvent 将事件翻译为生命周期记录并投递到消费队列。
func (l *LifecycleObserver) OnEvent(event observer.Event) error {
	switch ev := event.(type) {
	case observer.SeedInputEvent:
		return l.inlet.Send(LifecycleRecord{
			Kind:           lifecycleSeed,
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.SeedID,
			ParentIDs:      ev.Parents,
			PlotName:       ev.Plot,
			SeedJSON:       toLifecycleJSON(ev.Seed),
			TS:             unixSeconds(ev.Time),
		})

	case observer.SeedRipenEvent:
		return l.inlet.Send(LifecycleRecord{
			Kind:           lifecycleRipen,
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.FruitID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			FruitJSON:      toLifecycleJSON(ev.Fruit),
			TS:             unixSeconds(ev.Time),
		})

	case observer.SeedWitherEvent:
		return l.inlet.Send(LifecycleRecord{
			Kind:           lifecycleWither,
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.WeedID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			WitherType:     fmt.Sprintf("%T", ev.Err),
			WitherMessage:  fmt.Sprintf("%v", ev.Err),
			TS:             unixSeconds(ev.Time),
		})

	case observer.SeedPruneEvent:
		return l.inlet.Send(LifecycleRecord{
			Kind:           lifecyclePrune,
			InputEventID:   ev.SeedID,
			CurrentEventID: ev.PruneID,
			ParentIDs:      []int{ev.SeedID},
			PlotName:       ev.Plot,
			TS:             unixSeconds(ev.Time),
		})

	case observer.FarmStartEvent, observer.FarmEndEvent,
		observer.PlotStartEvent, observer.PlotEndEvent,
		observer.SeedReplantEvent,
		observer.SealInputEvent, observer.SealMergeEvent:
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
