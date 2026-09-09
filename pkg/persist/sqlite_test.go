package persist

import (
	"path/filepath"
	"testing"
)

// TestLifecycleSQLiteEventRoundTrip 验证事件和父边写入后可被完整读回。
func TestLifecycleSQLiteEventRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
	db, err := OpenLifecycleSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenLifecycleSQLite() error = %v", err)
	}
	defer db.Close()

	if err := InsertLifecycleEvent(db, 1, "seed", "source", 1.0, nil); err != nil {
		t.Fatalf("InsertLifecycleEvent(seed) error = %v", err)
	}

	if err := InsertLifecycleEvent(db, 2, "fruit", "source", 2.0, []int{1}); err != nil {
		t.Fatalf("InsertLifecycleEvent(fruit) error = %v", err)
	}

	loadedEvent, loadErr := LoadLifecycleEvent(db, 2)
	if loadErr != nil {
		t.Fatalf("LoadLifecycleEvent() error = %v", loadErr)
	}
	if loadedEvent.EventType != "fruit" {
		t.Fatalf("LoadLifecycleEvent() event type = %q, want %q", loadedEvent.EventType, "fruit")
	}

	parentIDs, parentErr := LoadLifecycleEventParents(db, 2)
	if parentErr != nil {
		t.Fatalf("LoadLifecycleEventParents() error = %v", parentErr)
	}
	if len(parentIDs) != 1 || parentIDs[0] != 1 {
		t.Fatalf("LoadLifecycleEventParents() = %v, want [1]", parentIDs)
	}
}

// TestLifecycleSQLiteStatusRoundTrip 验证状态快照的写入、更新和晋升。
func TestLifecycleSQLiteStatusRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
	db, err := OpenLifecycleSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenLifecycleSQLite() error = %v", err)
	}
	defer db.Close()

	for _, record := range []LifecycleEventRecord{
		{EventID: 1, EventType: "seed", Plot: "stage_a", TS: 1.0},
		{EventID: 3, EventType: "fruit", Plot: "stage_a", TS: 3.0},
	} {
		if err := InsertLifecycleEvent(db, record.EventID, record.EventType, record.Plot, record.TS, nil); err != nil {
			t.Fatalf("InsertLifecycleEvent(%d) error = %v", record.EventID, err)
		}
	}

	if err := UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0); err != nil {
		t.Fatalf("UpsertLifecycleStatusSeed() error = %v", err)
	}

	if err := PromoteLifecycleStatusRipen(db, 1, 3, `{"ok":true}`, 3.0); err != nil {
		t.Fatalf("PromoteLifecycleStatusRipen() error = %v", err)
	}

	loadedStatus, loadErr := LoadLifecycleStatus(db, 1)
	if loadErr != nil {
		t.Fatalf("LoadLifecycleStatus() error = %v", loadErr)
	}
	if loadedStatus.CurrentEventID != 3 {
		t.Fatalf("LoadLifecycleStatus() current event = %d, want %d", loadedStatus.CurrentEventID, 3)
	}
	if loadedStatus.Status != "ripen" {
		t.Fatalf("LoadLifecycleStatus() status = %q, want %q", loadedStatus.Status, "ripen")
	}
	if loadedStatus.ResultJSON != `{"ok":true}` {
		t.Fatalf("LoadLifecycleStatus() result json = %q, want %q", loadedStatus.ResultJSON, `{"ok":true}`)
	}
}

// TestLifecycleSQLiteStatusPairs 验证成功对和失败对查询。
func TestLifecycleSQLiteStatusPairs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lifecycle.sqlite3")
	db, err := OpenLifecycleSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenLifecycleSQLite() error = %v", err)
	}
	defer db.Close()

	for _, record := range []LifecycleEventRecord{
		{EventID: 1, EventType: "seed", Plot: "stage_a", TS: 1.0},
		{EventID: 2, EventType: "fruit", Plot: "stage_a", TS: 2.0},
		{EventID: 3, EventType: "seed", Plot: "stage_a", TS: 3.0},
		{EventID: 4, EventType: "weed", Plot: "stage_a", TS: 4.0},
		{EventID: 5, EventType: "seed", Plot: "stage_b", TS: 5.0},
		{EventID: 6, EventType: "fruit", Plot: "stage_b", TS: 6.0},
	} {
		if err := InsertLifecycleEvent(db, record.EventID, record.EventType, record.Plot, record.TS, nil); err != nil {
			t.Fatalf("InsertLifecycleEvent(%d) error = %v", record.EventID, err)
		}
	}

	if err := UpsertLifecycleStatusSeed(db, 1, `{"value":"alpha"}`, "stage_a", 1.0); err != nil {
		t.Fatalf("UpsertLifecycleStatusSeed(success seed) error = %v", err)
	}
	if err := PromoteLifecycleStatusRipen(db, 1, 2, `{"ok":true}`, 2.0); err != nil {
		t.Fatalf("PromoteLifecycleStatusRipen() error = %v", err)
	}

	if err := UpsertLifecycleStatusSeed(db, 3, `{"value":"beta"}`, "stage_a", 3.0); err != nil {
		t.Fatalf("UpsertLifecycleStatusSeed(failed seed) error = %v", err)
	}
	if err := PromoteLifecycleStatusWither(db, 3, 4, "*errors.errorString", "boom", 4.0); err != nil {
		t.Fatalf("PromoteLifecycleStatusWither() error = %v", err)
	}

	if err := UpsertLifecycleStatusSeed(db, 5, `{"value":"gamma"}`, "stage_b", 5.0); err != nil {
		t.Fatalf("UpsertLifecycleStatusSeed(other plot seed) error = %v", err)
	}
	if err := PromoteLifecycleStatusRipen(db, 5, 6, `{"ok":"other"}`, 6.0); err != nil {
		t.Fatalf("PromoteLifecycleStatusRipen(other plot) error = %v", err)
	}

	statuses, queryErr := LoadLifecycleStatuses(db, "stage_a")
	if queryErr != nil {
		t.Fatalf("LoadLifecycleStatuses() error = %v", queryErr)
	}
	if len(statuses) != 2 {
		t.Fatalf("LoadLifecycleStatuses() len = %d, want 2", len(statuses))
	}
	if statuses[0].TaskJSON != `{"value":"alpha"}` ||
		statuses[0].Status != "ripen" ||
		statuses[0].ResultJSON != `{"ok":true}` {
		t.Fatalf("LoadLifecycleStatuses()[0] = %#v, want task alpha/ripen", statuses[0])
	}
	if statuses[1].TaskJSON != `{"value":"beta"}` ||
		statuses[1].Status != "wither" ||
		statuses[1].ErrorType != "*errors.errorString" ||
		statuses[1].ErrorMessage != "boom" {
		t.Fatalf("LoadLifecycleStatuses()[1] = %#v, want task beta/wither", statuses[1])
	}
}
