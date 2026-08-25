package grow

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// LifecycleEventRecord 表示一条事件表记录。
type LifecycleEventRecord struct {
	EventID   int
	EventType string
	Plot      string
	TS        float64
}

// LifecycleStatusRecord 表示一条任务状态快照。
type LifecycleStatusRecord struct {
	InputEventID   int
	CurrentEventID int
	TaskJSON       string
	Plot           string
	Status         string
	ErrorType      string
	ErrorMessage   string
	ResultJSON     string
	TS             float64
}

const lifecycleSQLiteSchema = `
CREATE TABLE IF NOT EXISTS events (
    event_id INTEGER PRIMARY KEY,
    event_type TEXT NOT NULL,
    plot TEXT NOT NULL DEFAULT '',
    ts REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS event_parents (
    event_id INTEGER NOT NULL,
    parent_id INTEGER NOT NULL,
    PRIMARY KEY (event_id, parent_id),
    FOREIGN KEY (event_id) REFERENCES events(event_id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES events(event_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS status (
    input_event_id INTEGER PRIMARY KEY,
    current_event_id INTEGER NOT NULL,
    task_json TEXT NOT NULL,
    plot TEXT NOT NULL,
    status TEXT NOT NULL,
    error_type TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT 'null',
    ts REAL NOT NULL,
    FOREIGN KEY (input_event_id) REFERENCES events(event_id) ON DELETE CASCADE,
    FOREIGN KEY (current_event_id) REFERENCES events(event_id)
);

CREATE INDEX IF NOT EXISTS idx_events_type_ts ON events(event_type, ts);
CREATE INDEX IF NOT EXISTS idx_status_plot_status_ts ON status(plot, status, ts);
CREATE INDEX IF NOT EXISTS idx_status_current_event ON status(current_event_id);
`

// ==== events 表操作 ====

// InsertLifecycleEvent 写入一条事件记录及其父事件边。
func InsertLifecycleEvent(db *sql.DB, record LifecycleEventRecord, parentIDs []int) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin insert lifecycle event: %w", err)
	}
	defer rollbackOnError(tx)

	if _, err := tx.Exec(
		`INSERT INTO events (event_id, event_type, plot, ts) VALUES (?, ?, ?, ?)`,
		record.EventID,
		record.EventType,
		record.Plot,
		record.TS,
	); err != nil {
		return fmt.Errorf("insert event %d: %w", record.EventID, err)
	}

	for _, parentID := range parentIDs {
		if _, err := tx.Exec(
			`INSERT INTO event_parents (event_id, parent_id) VALUES (?, ?)`,
			record.EventID,
			parentID,
		); err != nil {
			return fmt.Errorf("insert parent edge %d->%d: %w", parentID, record.EventID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert lifecycle event: %w", err)
	}
	return nil
}

// ==== events 表查询 ====

// LoadLifecycleEvent 读取一条事件记录。
func LoadLifecycleEvent(db *sql.DB, eventID int) (LifecycleEventRecord, error) {
	var record LifecycleEventRecord
	err := db.QueryRow(
		`SELECT event_id, event_type, plot, ts FROM events WHERE event_id = ?`,
		eventID,
	).Scan(
		&record.EventID,
		&record.EventType,
		&record.Plot,
		&record.TS,
	)
	if err != nil {
		return LifecycleEventRecord{}, fmt.Errorf("load lifecycle event %d: %w", eventID, err)
	}
	return record, nil
}

// LoadLifecycleEventParents 按 event_id 读取全部父事件 ID。
func LoadLifecycleEventParents(db *sql.DB, eventID int) ([]int, error) {
	rows, err := db.Query(
		`SELECT parent_id FROM event_parents WHERE event_id = ? ORDER BY parent_id`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("query lifecycle event parents for %d: %w", eventID, err)
	}
	defer rows.Close()

	parentIDs := make([]int, 0)
	for rows.Next() {
		var parentID int
		if err := rows.Scan(&parentID); err != nil {
			return nil, fmt.Errorf("scan lifecycle event parent for %d: %w", eventID, err)
		}
		parentIDs = append(parentIDs, parentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lifecycle event parents for %d: %w", eventID, err)
	}

	return parentIDs, nil
}

// ==== status 表操作 ====

// UpsertLifecycleStatus 写入或覆盖一条当前状态快照。
func UpsertLifecycleStatus(db *sql.DB, record LifecycleStatusRecord) error {
	_, err := db.Exec(
		`
		INSERT INTO status (
			input_event_id, current_event_id, task_json, plot, status,
			error_type, error_message, result_json, ts
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(input_event_id) DO UPDATE SET
			current_event_id = excluded.current_event_id,
			task_json = excluded.task_json,
			plot = excluded.plot,
			status = excluded.status,
			error_type = excluded.error_type,
			error_message = excluded.error_message,
			result_json = excluded.result_json,
			ts = excluded.ts
		`,
		record.InputEventID,
		record.CurrentEventID,
		record.TaskJSON,
		record.Plot,
		record.Status,
		record.ErrorType,
		record.ErrorMessage,
		record.ResultJSON,
		record.TS,
	)
	if err != nil {
		return fmt.Errorf("upsert lifecycle status for input event %d: %w", record.InputEventID, err)
	}
	return nil
}

// PromoteLifecycleStatusSuccess 将一条状态快照晋升为成功。
func PromoteLifecycleStatusSuccess(db *sql.DB, inputEventID int, currentEventID int, resultJSON string, ts float64) error {
	_, err := db.Exec(
		`
		UPDATE status
		SET current_event_id = ?, status = 'success', result_json = ?, ts = ?
		WHERE input_event_id = ?
		`,
		currentEventID,
		resultJSON,
		ts,
		inputEventID,
	)
	if err != nil {
		return fmt.Errorf("promote lifecycle status success for input event %d: %w", inputEventID, err)
	}
	return nil
}

// PromoteLifecycleStatusFailed 将一条状态快照晋升为失败。
func PromoteLifecycleStatusFailed(
	db *sql.DB,
	inputEventID int,
	currentEventID int,
	errorType string,
	errorMessage string,
	ts float64,
) error {
	_, err := db.Exec(
		`
		UPDATE status
		SET current_event_id = ?, status = 'failed', error_type = ?, error_message = ?, ts = ?
		WHERE input_event_id = ?
		`,
		currentEventID,
		errorType,
		errorMessage,
		ts,
		inputEventID,
	)
	if err != nil {
		return fmt.Errorf("promote lifecycle status failed for input event %d: %w", inputEventID, err)
	}
	return nil
}

// ==== status 表查询 ====

// LoadLifecycleStatus 读取一条状态快照。
func LoadLifecycleStatus(db *sql.DB, inputEventID int) (LifecycleStatusRecord, error) {
	var record LifecycleStatusRecord
	err := db.QueryRow(
		`
		SELECT input_event_id, current_event_id, task_json, plot, status,
		       error_type, error_message, result_json, ts
		FROM status
		WHERE input_event_id = ?
		`,
		inputEventID,
	).Scan(
		&record.InputEventID,
		&record.CurrentEventID,
		&record.TaskJSON,
		&record.Plot,
		&record.Status,
		&record.ErrorType,
		&record.ErrorMessage,
		&record.ResultJSON,
		&record.TS,
	)
	if err != nil {
		return LifecycleStatusRecord{}, fmt.Errorf("load lifecycle status for input event %d: %w", inputEventID, err)
	}
	return record, nil
}

// LoadLifecycleStatuses 读取指定 plot 的全部任务状态快照。
func LoadLifecycleStatuses(db *sql.DB, plotName string) ([]LifecycleStatusRecord, error) {
	rows, err := db.Query(
		`
		SELECT input_event_id, current_event_id, task_json, plot, status,
		       error_type, error_message, result_json, ts
		FROM status
		WHERE plot = ?
		ORDER BY ts, input_event_id
		`,
		plotName,
	)
	if err != nil {
		return nil, fmt.Errorf("query lifecycle statuses for plot %q: %w", plotName, err)
	}
	defer rows.Close()

	records := make([]LifecycleStatusRecord, 0)
	for rows.Next() {
		var record LifecycleStatusRecord
		if err := rows.Scan(
			&record.InputEventID,
			&record.CurrentEventID,
			&record.TaskJSON,
			&record.Plot,
			&record.Status,
			&record.ErrorType,
			&record.ErrorMessage,
			&record.ResultJSON,
			&record.TS,
		); err != nil {
			return nil, fmt.Errorf("scan lifecycle status for plot %q: %w", plotName, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lifecycle statuses for plot %q: %w", plotName, err)
	}

	return records, nil
}

// ==== sqlite 配置与事务工具函数 ====

// OpenLifecycleSQLite 打开 sqlite 数据库并确保表结构存在。
func OpenLifecycleSQLite(dbPath string) (*sql.DB, error) {
	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = fmt.Sprintf("file:%s", filepath.ToSlash(dbPath))
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open lifecycle sqlite: %w", err)
	}

	if err := configureLifecycleSQLite(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := EnsureLifecycleSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// EnsureLifecycleSQLiteSchema 确保生命周期持久化所需的表和索引存在。
func EnsureLifecycleSQLiteSchema(db *sql.DB) error {
	if _, err := db.Exec(lifecycleSQLiteSchema); err != nil {
		return fmt.Errorf("ensure lifecycle sqlite schema: %w", err)
	}
	return nil
}

// configureLifecycleSQLite 初始化当前连接的 sqlite 运行参数。
func configureLifecycleSQLite(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("set sqlite journal mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous = NORMAL`); err != nil {
		return fmt.Errorf("set sqlite synchronous mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	return nil
}

// rollbackOnError 在调用方提前返回时回滚未提交事务。
func rollbackOnError(tx *sql.Tx) {
	_ = tx.Rollback()
}
