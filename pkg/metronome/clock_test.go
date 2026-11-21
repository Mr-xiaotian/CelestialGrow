package metronome

import (
	"errors"
	"io"
	"log"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestNewClockRejectsInvalidArguments(t *testing.T) {
	if _, err := NewClock(0, TickerFunc(func(TickEvent) error { return nil })); err == nil {
		t.Fatal("NewClock(0, root) = nil, want error")
	}
	if _, err := NewClock(time.Millisecond, nil); err == nil {
		t.Fatal("NewClock(interval, nil) = nil, want error")
	}
}

func TestClockTicksRoot(t *testing.T) {
	root := &channelTicker{ch: make(chan int, 4)}

	clock, err := NewClock(10*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() {
		if err := clock.Stop(); err != nil {
			t.Fatalf("Stop() = %v", err)
		}
	}()

	var got []int
	timeout := time.After(200 * time.Millisecond)
	for len(got) < 3 {
		select {
		case seq := <-root.ch:
			got = append(got, seq)
		case <-timeout:
			t.Fatalf("timed out waiting for ticks, got %v", got)
		}
	}

	want := []int{1, 2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("ticks = %v, want %v", got, want)
	}
}

func TestClockRespectsRootTickPeriod(t *testing.T) {
	root := &periodChannelTicker{
		period: 2,
		ch:     make(chan int, 4),
	}

	clock, err := NewClock(10*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() {
		if err := clock.Stop(); err != nil {
			t.Fatalf("Stop() = %v", err)
		}
	}()

	var got []int
	timeout := time.After(250 * time.Millisecond)
	for len(got) < 3 {
		select {
		case seq := <-root.ch:
			got = append(got, seq)
		case <-timeout:
			t.Fatalf("timed out waiting for root ticks, got %v", got)
		}
	}

	want := []int{1, 3, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("ticks = %v, want %v", got, want)
	}
}

func TestClockSetIntervalTakesEffectOnNextTick(t *testing.T) {
	root := &channelTicker{ch: make(chan int, 2)}

	clock, err := NewClock(200*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	defer func() {
		if err := clock.Stop(); err != nil {
			t.Fatalf("Stop() = %v", err)
		}
	}()

	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if err := clock.SetInterval(10 * time.Millisecond); err != nil {
		t.Fatalf("SetInterval() = %v", err)
	}

	select {
	case <-root.ch:
		if elapsed := time.Since(start); elapsed > 120*time.Millisecond {
			t.Fatalf("first tick after SetInterval took %s, want <= 120ms", elapsed)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("timed out waiting for first tick after SetInterval")
	}
}

func TestClockIdempotentStartStop(t *testing.T) {
	root := &channelTicker{ch: make(chan int, 1)}

	clock, err := NewClock(10*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("first Start() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("second Start() = %v", err)
	}
	if err := clock.Stop(); err != nil {
		t.Fatalf("first Stop() = %v", err)
	}
	if err := clock.Stop(); err != nil {
		t.Fatalf("second Stop() = %v", err)
	}
}

func TestClockUsesRootErrorHandler(t *testing.T) {
	var handled []error
	root := &clockHandlingProbe{
		onTick: func(TickEvent) error {
			return errors.New("boom")
		},
		onHandle: func(err error) {
			handled = append(handled, err)
		},
	}

	clock, err := NewClock(10*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	timeout := time.After(150 * time.Millisecond)
	for len(handled) == 0 {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for handled error")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if err := clock.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
}

func TestClockSurvivesRootErrorHandlerPanic(t *testing.T) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previous)

	root := &clockHandlingProbe{
		onTick: func(TickEvent) error {
			return errors.New("boom")
		},
		onHandle: func(error) {
			panic("handler boom")
		},
	}

	clock, err := NewClock(10*time.Millisecond, root)
	if err != nil {
		t.Fatalf("NewClock() = %v", err)
	}
	if err := clock.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	if !clock.IsRunning() {
		t.Fatal("clock stopped unexpectedly after handler panic")
	}
	if err := clock.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
}

type channelTicker struct {
	ch chan int
}

func (t *channelTicker) OnTick(event TickEvent) error {
	t.ch <- event.Seq
	return nil
}

type periodChannelTicker struct {
	period int
	ch     chan int
}

func (t *periodChannelTicker) TickPeriod() int {
	return t.period
}

func (t *periodChannelTicker) OnTick(event TickEvent) error {
	t.ch <- event.Seq
	return nil
}

type clockHandlingProbe struct {
	mu       sync.Mutex
	onTick   func(TickEvent) error
	onHandle func(error)
}

func (p *clockHandlingProbe) OnTick(event TickEvent) error {
	return p.onTick(event)
}

func (p *clockHandlingProbe) HandleTickError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onHandle(err)
}
