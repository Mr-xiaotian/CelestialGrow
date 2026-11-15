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

// ==== Lifecycle ====

// lifecycleRecorder 记录测试观察者的生命周期调用顺序。
type lifecycleRecorder struct {
	mu    sync.Mutex
	calls []string
}

// record 追加一次调用记录。
func (r *lifecycleRecorder) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// snapshot 返回调用记录副本。
func (r *lifecycleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// lifecycleWatcherStub 是可注入启动/停止错误的 Lifecycle 测试桩。
type lifecycleWatcherStub struct {
	name     string
	recorder *lifecycleRecorder
	startErr error
	stopErr  error
}

func (s *lifecycleWatcherStub) OnEvent(Event) error { return nil }

// Start 记录启动调用；注入错误时直接返回且不记录。
func (s *lifecycleWatcherStub) Start() error {
	if s.startErr != nil {
		return s.startErr
	}
	s.recorder.record("start:" + s.name)
	return nil
}

// Stop 记录停止调用并返回注入的错误。
func (s *lifecycleWatcherStub) Stop() error {
	s.recorder.record("stop:" + s.name)
	return s.stopErr
}

func TestHubStartsAndStopsLifecycleInOrder(t *testing.T) {
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	for _, name := range []string{"a", "b", "c"} {
		if err := hub.AddWatcher(&lifecycleWatcherStub{name: name, recorder: recorder}); err != nil {
			t.Fatalf("AddWatcher(%q) = %v", name, err)
		}
	}

	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := hub.Start(); err != nil {
		t.Fatalf("second Start() = %v", err)
	}
	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if err := hub.Stop(); err != nil {
		t.Fatalf("second Stop() = %v", err)
	}

	want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
}

func TestHubSkipsNonLifecycleWatcher(t *testing.T) {
	var events int
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	if err := hub.AddWatcher(WatcherFunc(func(Event) error {
		events++
		return nil
	})); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}
	if err := hub.AddWatcher(&lifecycleWatcherStub{name: "a", recorder: recorder}); err != nil {
		t.Fatalf("AddWatcher(lifecycle) = %v", err)
	}

	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if events != 0 {
		t.Fatalf("events = %d, want 0", events)
	}
	if got, want := recorder.snapshot(), []string{"start:a", "stop:a"}; !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}

	// 停止后观察者仍参与事件分发。
	if err := hub.OnEvent(PlotStartEvent{}); err != nil {
		t.Fatalf("OnEvent() = %v", err)
	}
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
}

func TestHubRollsBackOnStartFailure(t *testing.T) {
	boom := errors.New("boom")
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	for _, watcher := range []*lifecycleWatcherStub{
		{name: "a", recorder: recorder},
		{name: "b", recorder: recorder, startErr: boom},
		{name: "c", recorder: recorder},
	} {
		if err := hub.AddWatcher(watcher); err != nil {
			t.Fatalf("AddWatcher(%q) = %v", watcher.name, err)
		}
	}

	err := hub.Start()
	if err == nil {
		t.Fatal("Start() = nil, want error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want wrap boom", err)
	}
	if !strings.Contains(err.Error(), "start") {
		t.Fatalf("Start() error lacks context: %v", err)
	}

	want := []string{"start:a", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}

	// 启动失败即终态：Stop 无副作用，且不可再次启动。
	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if err := hub.Start(); err == nil {
		t.Fatal("Start() after failed Start = nil, want error")
	}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls after Stop = %v, want %v", got, want)
	}
}

func TestHubStopBeforeStartIsNoop(t *testing.T) {
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	if err := hub.AddWatcher(&lifecycleWatcherStub{name: "a", recorder: recorder}); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if calls := recorder.snapshot(); len(calls) != 0 {
		t.Fatalf("lifecycle calls = %v, want none", calls)
	}
}

func TestHubRejectsStartAfterStop(t *testing.T) {
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	if err := hub.AddWatcher(&lifecycleWatcherStub{name: "a", recorder: recorder}); err != nil {
		t.Fatalf("AddWatcher() = %v", err)
	}

	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if err := hub.Start(); err == nil {
		t.Fatal("Start() after Stop = nil, want error")
	}

	want := []string{"start:a", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
}

func TestHubStartsNestedHub(t *testing.T) {
	recorder := &lifecycleRecorder{}
	child := NewHub()
	if err := child.AddWatcher(&lifecycleWatcherStub{name: "child", recorder: recorder}); err != nil {
		t.Fatalf("child.AddWatcher() = %v", err)
	}

	parent := NewHub()
	if err := parent.AddWatcher(child); err != nil {
		t.Fatalf("AddWatcher(child) = %v", err)
	}
	if err := parent.AddWatcher(&lifecycleWatcherStub{name: "parent", recorder: recorder}); err != nil {
		t.Fatalf("AddWatcher(parent) = %v", err)
	}

	if err := parent.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := parent.Start(); err != nil {
		t.Fatalf("second Start() = %v", err)
	}
	if err := parent.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	want := []string{"start:child", "start:parent", "stop:parent", "stop:child"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
}

func TestHubJoinsStopErrors(t *testing.T) {
	errA := errors.New("a boom")
	errB := errors.New("b boom")
	recorder := &lifecycleRecorder{}
	hub := NewHub()
	for _, watcher := range []*lifecycleWatcherStub{
		{name: "a", recorder: recorder, stopErr: errA},
		{name: "b", recorder: recorder, stopErr: errB},
	} {
		if err := hub.AddWatcher(watcher); err != nil {
			t.Fatalf("AddWatcher(%q) = %v", watcher.name, err)
		}
	}

	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	err := hub.Stop()
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("Stop() error = %v, want both stop errors", err)
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
