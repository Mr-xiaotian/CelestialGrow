package scarecrow

import (
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ==== Dispatch ====

func TestHubFansOutInRegistrationOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string

	hub := NewHub(WithErrorHandler(func(error) {}))
	for _, tag := range []string{"a", "b", "c"} {
		tag := tag
		err := hub.AddWatcher(WatcherFunc(func(Event) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, tag)
			return nil
		}))
		if err != nil {
			t.Fatalf("AddWatcher(%q): %v", tag, err)
		}
	}

	if err := hub.OnEvent(PlotStartEvent{Plot: "only"}); err != nil {
		t.Fatalf("OnEvent() = %v, want nil", err)
	}

	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

func TestHubAllowsDuplicateRegistration(t *testing.T) {
	var calls int
	listener := WatcherFunc(func(Event) error {
		calls++
		return nil
	})

	hub := NewHub()
	if err := hub.AddWatcher(listener); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(listener); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	_ = hub.OnEvent(PlotStartEvent{})

	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestHubWithoutWatchers(t *testing.T) {
	if err := NewHub().OnEvent(PlotStartEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v, want nil", err)
	}
}

func TestHubIgnoresNilEvent(t *testing.T) {
	var called bool
	hub := NewHub()
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		called = true
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.OnEvent(nil); err != nil {
		t.Fatalf("OnEvent(nil) = %v, want nil", err)
	}
	if called {
		t.Fatal("watcher must not be called for nil event")
	}
}

// ==== Registration ====

func TestHubRejectsNilWatcher(t *testing.T) {
	if err := NewHub().AddWatcher(nil); err == nil {
		t.Fatal("AddWatcher(nil) = nil, want error")
	}
}

func TestHubNestsChildHub(t *testing.T) {
	var calls int
	child := NewHub(WithErrorHandler(func(error) {}))
	if err := child.AddWatcher(WatcherFunc(func(Event) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	parent := NewHub(WithErrorHandler(func(error) {}))
	if err := parent.AddWatcher(child); err != nil {
		t.Fatalf("AddWatcher(child) = %v", err)
	}
	if err := parent.OnEvent(PlotStartEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestHubRejectsCyclicRegistration(t *testing.T) {
	a, b := NewHub(), NewHub()

	if err := a.AddWatcher(a); err == nil {
		t.Fatal("self registration must be rejected")
	}
	if err := a.AddWatcher(b); err != nil {
		t.Fatalf("AddWatcher(b) = %v", err)
	}
	if err := b.AddWatcher(a); err == nil {
		t.Fatal("indirect cycle must be rejected")
	}
}

// ==== Error Isolation ====

func TestHubIsolatesWatcherError(t *testing.T) {
	boom := errors.New("boom")
	var reported []error
	var calls int

	hub := NewHub(WithErrorHandler(func(err error) {
		reported = append(reported, err)
	}))
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		return boom
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.OnEvent(SeedRipenEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("healthy watcher calls = %d, want 1", calls)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %d errors, want 1", len(reported))
	}
	if !errors.Is(reported[0], boom) {
		t.Fatalf("reported error does not wrap boom: %v", reported[0])
	}
	if !strings.Contains(reported[0].Error(), "scarecrow") {
		t.Fatalf("reported error lacks context: %v", reported[0])
	}
}

func TestHubRecoversWatcherPanic(t *testing.T) {
	var reported []error
	var calls int

	hub := NewHub(WithErrorHandler(func(err error) {
		reported = append(reported, err)
	}))
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		panic("kaboom")
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.OnEvent(PlotEndEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("healthy watcher calls = %d, want 1", calls)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "kaboom") {
		t.Fatalf("reported = %v, want one panic error", reported)
	}
}

func TestHubSurvivesErrorHandlerPanic(t *testing.T) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previous)

	var calls int
	hub := NewHub(WithErrorHandler(func(error) {
		panic("handler boom")
	}))
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		return errors.New("boom")
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.OnEvent(PlotStartEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("healthy watcher calls = %d, want 1", calls)
	}
}

func TestZeroValueHubIsUsable(t *testing.T) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previous)

	var calls int
	hub := &Hub{}
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		return errors.New("boom")
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.OnEvent(PlotStartEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	if calls != 1 {
		t.Fatalf("healthy watcher calls = %d, want 1", calls)
	}
}

// ==== Lookup ====

// markerWatcher 用于类型查找测试。
type markerWatcher struct {
	tag string
}

func (m *markerWatcher) OnEvent(Event) error { return nil }

func TestFindFindsDirectWatcher(t *testing.T) {
	hub := NewHub()
	marker := &markerWatcher{tag: "direct"}
	if err := hub.AddWatcher(marker); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	found, ok := Find[*markerWatcher](hub)
	if !ok {
		t.Fatal("Find() = false, want true")
	}
	if found != marker {
		t.Fatalf("Find() = %#v, want %#v", found, marker)
	}
}

func TestFindSearchesNestedHub(t *testing.T) {
	child := NewHub()
	marker := &markerWatcher{tag: "nested"}
	if err := child.AddWatcher(marker); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	parent := NewHub()
	if err := parent.AddWatcher(child); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	found, ok := Find[*markerWatcher](parent)
	if !ok || found != marker {
		t.Fatalf("Find() = (%#v, %v), want (%#v, true)", found, ok, marker)
	}
}

func TestFindPrefersDirectWatcher(t *testing.T) {
	direct := &markerWatcher{tag: "direct"}
	nested := &markerWatcher{tag: "nested"}

	child := NewHub()
	if err := child.AddWatcher(nested); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	parent := NewHub()
	if err := parent.AddWatcher(child); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := parent.AddWatcher(direct); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	found, ok := Find[*markerWatcher](parent)
	if !ok || found != direct {
		t.Fatalf("Find() = (%#v, %v), want (%#v, true)", found, ok, direct)
	}
}

func TestFindMissingWatcher(t *testing.T) {
	if _, ok := Find[*markerWatcher](NewHub()); ok {
		t.Fatal("Find() = true, want false")
	}

	var nilHub *Hub
	if _, ok := Find[*markerWatcher](nilHub); ok {
		t.Fatal("Find(nil) = true, want false")
	}
}

// ==== Concurrency ====

func TestHubConcurrentAddAndEmit(t *testing.T) {
	const (
		watcherCount = 64
		eventCount   = 128
	)

	var calls atomic.Int64
	hub := NewHub(WithErrorHandler(func(error) {}))

	var wg sync.WaitGroup
	for range watcherCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := hub.AddWatcher(WatcherFunc(func(Event) error {
				calls.Add(1)
				return nil
			}))
			if err != nil {
				t.Errorf("AddWatcher() = %v", err)
			}
		}()
	}
	for range eventCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := hub.OnEvent(PlotStartEvent{Plot: "only"}); err != nil {
				t.Errorf("OnEvent() = %v", err)
			}
		}()
	}
	wg.Wait()

	hub.mu.RLock()
	registered := len(hub.listeners)
	hub.mu.RUnlock()
	if registered != watcherCount {
		t.Fatalf("registered listeners = %d, want %d", registered, watcherCount)
	}

	calls.Store(0)
	if err := hub.OnEvent(PlotStartEvent{Plot: "only"}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	if got := calls.Load(); got != watcherCount {
		t.Fatalf("calls = %d, want %d", got, watcherCount)
	}
}
