package metronome

import (
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestHubFansOutInRegistrationOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string

	hub, err := NewHub(WithErrorHandler(func(error) {}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}

	for _, tag := range []string{"a", "b", "c"} {
		tag := tag
		err := hub.AddTicker(TickerFunc(func(TickEvent) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, tag)
			return nil
		}))
		if err != nil {
			t.Fatalf("AddTicker(%q) = %v", tag, err)
		}
	}

	if err := hub.OnTick(TickEvent{Seq: 1}); err != nil {
		t.Fatalf("OnTick() = %v, want nil", err)
	}

	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

func TestHubFiltersByTickerPeriod(t *testing.T) {
	odd := &periodProbe{period: 2}
	all := &periodProbe{period: 1}

	hub, err := NewHub(WithErrorHandler(func(error) {}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(odd); err != nil {
		t.Fatalf("AddTicker(odd) = %v", err)
	}
	if err := hub.AddTicker(all); err != nil {
		t.Fatalf("AddTicker(all) = %v", err)
	}

	for seq := 1; seq <= 4; seq++ {
		_ = hub.OnTick(TickEvent{Seq: seq})
	}

	if got, want := odd.snapshot(), []int{1, 3}; !slices.Equal(got, want) {
		t.Fatalf("odd ticks = %v, want %v", got, want)
	}
	if got, want := all.snapshot(), []int{1, 2, 3, 4}; !slices.Equal(got, want) {
		t.Fatalf("all ticks = %v, want %v", got, want)
	}
}

func TestHubNestsChildHub(t *testing.T) {
	var calls int

	child, err := NewHub(WithErrorHandler(func(error) {}))
	if err != nil {
		t.Fatalf("NewHub(child) = %v", err)
	}
	if err := child.AddTicker(TickerFunc(func(TickEvent) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("child.AddTicker() = %v", err)
	}

	parent, err := NewHub(WithErrorHandler(func(error) {}))
	if err != nil {
		t.Fatalf("NewHub(parent) = %v", err)
	}
	if err := parent.AddTicker(child); err != nil {
		t.Fatalf("parent.AddTicker(child) = %v", err)
	}

	_ = parent.OnTick(TickEvent{Seq: 1})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestHubRejectsCyclicRegistration(t *testing.T) {
	a, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub(a) = %v", err)
	}
	b, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub(b) = %v", err)
	}

	if err := a.AddTicker(a); err == nil {
		t.Fatal("self registration must be rejected")
	}
	if err := a.AddTicker(b); err != nil {
		t.Fatalf("a.AddTicker(b) = %v", err)
	}
	if err := b.AddTicker(a); err == nil {
		t.Fatal("indirect cycle must be rejected")
	}
}

func TestHubRejectsDuplicateRegistration(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}

	ticker := &periodProbe{period: 1}
	if err := hub.AddTicker(ticker); err != nil {
		t.Fatalf("first AddTicker() = %v", err)
	}
	if err := hub.AddTicker(ticker); err == nil {
		t.Fatal("duplicate registration must be rejected")
	}
}

func TestHubIsolatesTickerError(t *testing.T) {
	boom := errors.New("boom")
	var reported []error
	var calls int

	hub, err := NewHub(WithErrorHandler(func(err error) {
		reported = append(reported, err)
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		return boom
	})); err != nil {
		t.Fatalf("AddTicker(boom) = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddTicker(healthy) = %v", err)
	}

	_ = hub.OnTick(TickEvent{Seq: 2})
	if calls != 1 {
		t.Fatalf("healthy calls = %d, want 1", calls)
	}
	if len(reported) != 1 {
		t.Fatalf("reported = %d errors, want 1", len(reported))
	}
	if !errors.Is(reported[0], boom) {
		t.Fatalf("reported error does not wrap boom: %v", reported[0])
	}
}

func TestHubRecoversTickerPanic(t *testing.T) {
	var reported []error
	var calls int

	hub, err := NewHub(WithErrorHandler(func(err error) {
		reported = append(reported, err)
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		panic("kaboom")
	})); err != nil {
		t.Fatalf("AddTicker(panic) = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddTicker(healthy) = %v", err)
	}

	_ = hub.OnTick(TickEvent{Seq: 1})
	if calls != 1 {
		t.Fatalf("healthy calls = %d, want 1", calls)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "kaboom") {
		t.Fatalf("reported = %v, want one panic error", reported)
	}
}

func TestHubUsesTickerErrorHandlerBeforeDefaultReporter(t *testing.T) {
	var handled []error
	var reported []error

	ticker := &handlingProbe{
		onTick: func(TickEvent) error {
			return errors.New("boom")
		},
		onHandle: func(err error) {
			handled = append(handled, err)
		},
	}

	hub, err := NewHub(WithErrorHandler(func(err error) {
		reported = append(reported, err)
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(ticker); err != nil {
		t.Fatalf("AddTicker() = %v", err)
	}

	_ = hub.OnTick(TickEvent{Seq: 1})
	if len(handled) != 1 {
		t.Fatalf("handled = %d errors, want 1", len(handled))
	}
	if len(reported) != 0 {
		t.Fatalf("reported = %v, want no fallback reporting", reported)
	}
}

func TestHubSurvivesDefaultErrorHandlerPanic(t *testing.T) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previous)

	var calls int
	hub, err := NewHub(WithErrorHandler(func(error) {
		panic("handler boom")
	}))
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		return errors.New("boom")
	})); err != nil {
		t.Fatalf("AddTicker(boom) = %v", err)
	}
	if err := hub.AddTicker(TickerFunc(func(TickEvent) error {
		calls++
		return nil
	})); err != nil {
		t.Fatalf("AddTicker(healthy) = %v", err)
	}

	_ = hub.OnTick(TickEvent{Seq: 1})
	if calls != 1 {
		t.Fatalf("healthy calls = %d, want 1", calls)
	}
}

func TestHubLifecycleStartStopOrder(t *testing.T) {
	recorder := &lifecycleRecorder{}
	a := &lifecycleProbe{name: "a", recorder: recorder}
	b := &lifecycleProbe{name: "b", recorder: recorder}

	hub, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(a); err != nil {
		t.Fatalf("AddTicker(a) = %v", err)
	}
	if err := hub.AddTicker(b); err != nil {
		t.Fatalf("AddTicker(b) = %v", err)
	}

	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := hub.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}

	want := []string{"start:a", "start:b", "stop:b", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestHubStartRollbackOnFailure(t *testing.T) {
	recorder := &lifecycleRecorder{}
	a := &lifecycleProbe{name: "a", recorder: recorder}
	b := &lifecycleProbe{name: "b", recorder: recorder, startErr: errors.New("boom")}

	hub, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(a); err != nil {
		t.Fatalf("AddTicker(a) = %v", err)
	}
	if err := hub.AddTicker(b); err != nil {
		t.Fatalf("AddTicker(b) = %v", err)
	}

	if err := hub.Start(); err == nil {
		t.Fatal("Start() = nil, want error")
	}

	want := []string{"start:a", "start:b", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestHubAddTickerStartsImmediatelyWhenRunning(t *testing.T) {
	recorder := &lifecycleRecorder{}
	a := &lifecycleProbe{name: "a", recorder: recorder}

	hub, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() { _ = hub.Stop() }()

	if err := hub.AddTicker(a); err != nil {
		t.Fatalf("AddTicker(a) = %v", err)
	}

	if got, want := recorder.snapshot(), []string{"start:a"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestHubRemoveTickerStopsImmediatelyWhenRunning(t *testing.T) {
	recorder := &lifecycleRecorder{}
	a := &lifecycleProbe{name: "a", recorder: recorder}

	hub, err := NewHub()
	if err != nil {
		t.Fatalf("NewHub() = %v", err)
	}
	if err := hub.AddTicker(a); err != nil {
		t.Fatalf("AddTicker(a) = %v", err)
	}
	if err := hub.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() { _ = hub.Stop() }()

	if err := hub.RemoveTicker(a); err != nil {
		t.Fatalf("RemoveTicker(a) = %v", err)
	}

	want := []string{"start:a", "stop:a"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

type periodProbe struct {
	mu     sync.Mutex
	period int
	seqs   []int
}

func (p *periodProbe) TickPeriod() int {
	return p.period
}

func (p *periodProbe) OnTick(event TickEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seqs = append(p.seqs, event.Seq)
	return nil
}

func (p *periodProbe) snapshot() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.seqs...)
}

type handlingProbe struct {
	onTick   func(TickEvent) error
	onHandle func(error)
}

func (p *handlingProbe) OnTick(event TickEvent) error {
	return p.onTick(event)
}

func (p *handlingProbe) HandleTickError(err error) {
	p.onHandle(err)
}

type lifecycleRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *lifecycleRecorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *lifecycleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type lifecycleProbe struct {
	name     string
	recorder *lifecycleRecorder
	startErr error
	stopErr  error
}

func (p *lifecycleProbe) OnTick(TickEvent) error {
	return nil
}

func (p *lifecycleProbe) Start() error {
	p.recorder.add("start:" + p.name)
	return p.startErr
}

func (p *lifecycleProbe) Stop() error {
	p.recorder.add("stop:" + p.name)
	return p.stopErr
}
