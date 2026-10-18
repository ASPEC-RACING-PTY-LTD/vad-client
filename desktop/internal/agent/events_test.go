package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func recv(t *testing.T, s Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-s.C():
		if !ok {
			t.Fatal("subscription closed")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return Event{}
}

func noEvent(t *testing.T, s Subscription) {
	t.Helper()
	select {
	case ev, ok := <-s.C():
		if ok {
			t.Fatalf("unexpected event %s", ev.Kind)
		}
	default:
	}
}

func TestBusFanOutAndFilter(t *testing.T) {
	b := NewBus(8)
	all := b.Subscribe()
	notes := b.Subscribe(KindNotification)
	conns := b.Subscribe(KindConnection)
	defer all.Close()
	defer notes.Close()
	defer conns.Close()

	if err := b.Publish(KindNotification, Notification{Title: "t", Message: "m", Level: "info"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Subscription{all, notes} {
		ev := recv(t, s)
		var n Notification
		if ev.Kind != KindNotification || ev.Seq != 1 || ev.Decode(&n) != nil || n.Title != "t" {
			t.Fatalf("bad event %+v / %+v", ev, n)
		}
	}
	noEvent(t, conns)
	if !b.HasSubscribers(KindConnection) || b.HasSubscribers(KindSnapshot) == false {
		t.Fatal("HasSubscribers: the unfiltered subscriber wants every kind")
	}
}

func TestBusSlowSubscriberDropsOldest(t *testing.T) {
	b := NewBus(4)
	slow := b.Subscribe()
	fast := b.Subscribe()
	defer slow.Close()
	got := make(chan int, 100)
	go func() {
		for ev := range fast.C() {
			var p ProgressEvent
			_ = ev.Decode(&p)
			got <- int(p.Done)
		}
		close(got)
	}()
	for i := 1; i <= 10; i++ {
		if err := b.Publish(KindProgress, ProgressEvent{Job: "test", Done: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// The slow subscriber keeps only the newest four, in order.
	if d := slow.Dropped(); d != 6 {
		t.Fatalf("dropped = %d, want 6", d)
	}
	for want := 7; want <= 10; want++ {
		var p ProgressEvent
		if err := recv(t, slow).Decode(&p); err != nil || int(p.Done) != want {
			t.Fatalf("got %d, want %d", p.Done, want)
		}
	}
	noEvent(t, slow)
	// The fast subscriber was never blocked by the slow one.
	fast.Close()
	n := 0
	for range got {
		n++
	}
	if n+int(fast.Dropped()) != 10 {
		t.Fatalf("fast subscriber: %d received + %d dropped != 10", n, fast.Dropped())
	}
}

func TestBusCloseAndUnsubscribe(t *testing.T) {
	b := NewBus(0)
	s := b.Subscribe()
	s.Close()
	s.Close() // idempotent
	if _, ok := <-s.C(); ok {
		t.Fatal("channel should be closed after Close")
	}
	if b.HasSubscribers(KindLog) {
		t.Fatal("closed subscription still registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cs := b.SubscribeContext(ctx, KindLog)
	cancel()
	select {
	case _, ok := <-cs.C():
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancel did not close the subscription")
	}
	live := b.Subscribe()
	b.Close()
	if _, ok := <-live.C(); ok {
		t.Fatal("bus Close should close subscriptions")
	}
	if err := b.Publish(KindLog, LogEvent{}); err != nil {
		t.Fatal("publish after close should be a no-op")
	}
	late := b.Subscribe()
	if _, ok := <-late.C(); ok {
		t.Fatal("subscribing to a closed bus yields a closed channel")
	}
}

func TestBusPublishUnmarshalable(t *testing.T) {
	b := NewBus(1)
	if err := b.Publish(KindLog, make(chan int)); err == nil {
		t.Fatal("expected a marshal error")
	}
}

func TestLogHandler(t *testing.T) {
	b := NewBus(8)
	s := b.Subscribe(KindLog)
	defer s.Close()
	log := slog.New(NewLogHandler(b, nil, slog.LevelInfo)).With("cfg", "a").WithGroup("g")
	log.Debug("hidden")
	log.Warn("shown", "n", 3)
	var le LogEvent
	if err := recv(t, s).Decode(&le); err != nil {
		t.Fatal(err)
	}
	if le.Message != "shown" || le.Level != "WARN" || le.Attrs["cfg"] != "a" || le.Attrs["g.n"] != "3" {
		t.Fatalf("bad log event %+v", le)
	}
	noEvent(t, s)
}
