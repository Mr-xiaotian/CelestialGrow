package scarecrow

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintWatcherReportsProgress(t *testing.T) {
	var out bytes.Buffer
	watcher := newPrintWatcher(&out, "demo")

	events := []Event{
		PlotStartEvent{},
		SeedInputEvent{},
		SeedInputEvent{},
		SeedInputEvent{},
		SeedRipenEvent{},
		SeedWitherEvent{},
		SeedPruneEvent{},
		PlotEndEvent{},
	}
	for _, event := range events {
		if err := watcher.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	text := out.String()
	want := []string{
		"[demo] start total=0",
		"[demo] total=1(+1)",
		"[demo] total=2(+1)",
		"[demo] total=3(+1)",
		"[demo] succeeded=1(+1), total=3",
		"[demo] failed=1(+1), total=3",
		"[demo] skipped=1(+1), total=3",
		"[demo] finish total=3, skipped=1, succeeded=1, failed=1",
	}
	for _, line := range want {
		if !strings.Contains(text, line) {
			t.Fatalf("output does not contain %q:\n%s", line, text)
		}
	}
}

func TestPrintWatcherIgnoresFarmAndSealEvents(t *testing.T) {
	var out bytes.Buffer
	watcher := newPrintWatcher(&out, "demo")

	for _, event := range []Event{
		FarmStartEvent{},
		FarmEndEvent{},
		SeedReplantEvent{},
		SealInputEvent{},
		SealMergeEvent{},
	} {
		if err := watcher.OnEvent(event); err != nil {
			t.Fatalf("OnEvent(%T) = %v", event, err)
		}
	}

	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
}
