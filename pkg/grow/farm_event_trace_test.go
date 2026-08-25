package grow

import "testing"

// TestFarmEventTraceLinear 验证可基于 events 和 event_parents 从下游最终事件追溯到上游输入事件。
func TestFarmEventTraceLinear(t *testing.T) {
	root := NewPlot("root", func(seed int) (int, error) {
		return seed * 2, nil
	}, WithTends(1))
	head := NewPlot("head", func(seed int) (int, error) {
		return seed + 1, nil
	}, WithTends(1))

	farm := NewFarm("event_trace_linear", "INFO")
	if err := farm.AddPlot(root, head); err != nil {
		t.Fatalf("AddPlot() error = %v", err)
	}
	if err := farm.Connect([]PlotNode{root}, []PlotNode{head}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := farm.Run(map[string][]any{
		"root": {3},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	handler, ok := farm.lifecycleSpout.Handler().(*LifecycleRecordHandler)
	if !ok {
		t.Fatalf("lifecycle handler type assertion failed")
	}
	if handler.SQLitePath == "" {
		t.Fatal("lifecycle sqlite path is empty")
	}

	db, err := OpenLifecycleSQLite(handler.SQLitePath)
	if err != nil {
		t.Fatalf("OpenLifecycleSQLite() error = %v", err)
	}
	defer db.Close()

	rootStatuses, err := LoadLifecycleStatuses(db, "root")
	if err != nil {
		t.Fatalf("LoadLifecycleStatuses(root) error = %v", err)
	}
	if len(rootStatuses) != 1 {
		t.Fatalf("len(rootStatuses) = %d, want 1", len(rootStatuses))
	}

	headStatuses, err := LoadLifecycleStatuses(db, "head")
	if err != nil {
		t.Fatalf("LoadLifecycleStatuses(head) error = %v", err)
	}
	if len(headStatuses) != 1 {
		t.Fatalf("len(headStatuses) = %d, want 1", len(headStatuses))
	}

	// head 的最终 fruit 事件应该可回溯为：
	// head fruit <- head seed <- root fruit <- root seed
	traceIDs := []int{
		headStatuses[0].CurrentEventID,
		headStatuses[0].InputEventID,
		rootStatuses[0].CurrentEventID,
		rootStatuses[0].InputEventID,
	}
	traceWants := []struct {
		eventType string
		plot      string
		parentID  int
	}{
		{eventType: "fruit", plot: "head", parentID: traceIDs[1]},
		{eventType: "seed", plot: "head", parentID: traceIDs[2]},
		{eventType: "fruit", plot: "root", parentID: traceIDs[3]},
		{eventType: "seed", plot: "root", parentID: 0},
	}

	for i, eventID := range traceIDs {
		record, err := LoadLifecycleEvent(db, eventID)
		if err != nil {
			t.Fatalf("LoadLifecycleEvent(%d) error = %v", eventID, err)
		}
		if record.EventType != traceWants[i].eventType {
			t.Fatalf("event %d type = %q, want %q", eventID, record.EventType, traceWants[i].eventType)
		}
		if record.Plot != traceWants[i].plot {
			t.Fatalf("event %d plot = %q, want %q", eventID, record.Plot, traceWants[i].plot)
		}

		parentIDs, err := LoadLifecycleEventParents(db, eventID)
		if err != nil {
			t.Fatalf("LoadLifecycleEventParents(%d) error = %v", eventID, err)
		}

		if traceWants[i].parentID == 0 {
			if len(parentIDs) != 0 {
				t.Fatalf("event %d parents = %v, want none", eventID, parentIDs)
			}
			continue
		}
		if len(parentIDs) != 1 || parentIDs[0] != traceWants[i].parentID {
			t.Fatalf("event %d parents = %v, want [%d]", eventID, parentIDs, traceWants[i].parentID)
		}
	}
}
