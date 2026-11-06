package sse

import (
	"sync"
	"testing"
	"time"
)

// drain reads everything currently buffered on ch without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestHubFanOut(t *testing.T) {
	h := NewHub()
	defer h.Close()

	const n = 8
	chans := make([]<-chan Event, n)
	for i := range chans {
		ch, cancel := h.Subscribe()
		defer cancel()
		chans[i] = ch
	}
	if got := h.SubscriberCount(); got != n {
		t.Fatalf("SubscriberCount = %d, want %d", got, n)
	}

	h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})

	for i, ch := range chans {
		got := drain(ch)
		if len(got) != 1 {
			t.Fatalf("subscriber %d got %d events, want 1", i, len(got))
		}
		if got[0].Type != TypeDBInvalidated {
			t.Errorf("subscriber %d: type = %q", i, got[0].Type)
		}
		payload, ok := got[0].Data.(DBInvalidated)
		if !ok || payload.Project != "loom" {
			t.Errorf("subscriber %d: data = %#v", i, got[0].Data)
		}
	}
}

func TestProjectsChangedContract(t *testing.T) {
	if TypeProjectsChanged != "projects-changed" {
		t.Fatalf("TypeProjectsChanged = %q", TypeProjectsChanged)
	}
	payload := ProjectsChanged{Added: []string{"new"}, Removed: []string{"old"}}
	if payload.Added[0] != "new" || payload.Removed[0] != "old" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestHubEventIDsAreMonotonic(t *testing.T) {
	h := NewHub()
	defer h.Close()
	ch, cancel := h.Subscribe()
	defer cancel()

	for i := 0; i < 5; i++ {
		h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})
	}

	got := drain(ch)
	if len(got) != 5 {
		t.Fatalf("got %d events, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID <= got[i-1].ID {
			t.Errorf("event ids not increasing: %d then %d", got[i-1].ID, got[i].ID)
		}
	}
	if h.LastID() != got[len(got)-1].ID {
		t.Errorf("LastID = %d, want %d", h.LastID(), got[len(got)-1].ID)
	}
}

// TestHubDropsOldestForSlowSubscriber is the buffer policy: a client that stops
// reading loses its oldest events and keeps the newest, and must never stall
// the publisher.
func TestHubDropsOldestForSlowSubscriber(t *testing.T) {
	h := NewHub()
	defer h.Close()

	ch, cancel := h.Subscribe()
	defer cancel()

	const over = SubscriberBuffer + 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < over; i++ {
			h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	got := drain(ch)
	if len(got) != SubscriberBuffer {
		t.Errorf("buffered %d events, want the buffer size %d", len(got), SubscriberBuffer)
	}
	if h.Dropped() == 0 {
		t.Error("Dropped() = 0, want the discarded events to be counted")
	}
	// The retained events must be the newest ones.
	if len(got) > 0 {
		newest := got[len(got)-1].ID
		if newest != uint64(over) {
			t.Errorf("newest retained id = %d, want %d (drop-oldest keeps the tail)", newest, over)
		}
		oldest := got[0].ID
		if oldest <= 1 {
			t.Errorf("oldest retained id = %d, want the early events to have been dropped", oldest)
		}
	}
}

func TestHubUnsubscribeClosesChannel(t *testing.T) {
	h := NewHub()
	defer h.Close()

	ch, cancel := h.Subscribe()
	if h.SubscriberCount() != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", h.SubscriberCount())
	}
	cancel()
	if h.SubscriberCount() != 0 {
		t.Errorf("SubscriberCount = %d after cancel, want 0", h.SubscriberCount())
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Error("channel was not closed")
	}

	// Cancel must be idempotent.
	cancel()
	cancel()

	// Publishing to a hub with no subscribers must not panic.
	h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})
}

func TestHubCloseEndsAllSubscriptions(t *testing.T) {
	h := NewHub()

	ch1, cancel1 := h.Subscribe()
	ch2, cancel2 := h.Subscribe()
	defer cancel1()
	defer cancel2()

	h.Close()

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Errorf("subscriber %d channel should be closed", i)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d channel was not closed", i)
		}
	}
	if h.SubscriberCount() != 0 {
		t.Errorf("SubscriberCount = %d after Close", h.SubscriberCount())
	}

	// Publish and Subscribe after Close must be safe no-ops.
	h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})
	ch, cancel := h.Subscribe()
	defer cancel()
	if _, ok := <-ch; ok {
		t.Error("Subscribe after Close should return a closed channel")
	}
	h.Close() // idempotent
}

// TestHubConcurrentPublishSubscribe is the -race workhorse: publishers,
// subscribers and cancellations all running at once.
func TestHubConcurrentPublishSubscribe(t *testing.T) {
	h := NewHub()
	defer h.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.Publish(TypeDBInvalidated, DBInvalidated{Project: "loom"})
				}
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch, cancel := h.Subscribe()
				for j := 0; j < 3; j++ {
					select {
					case <-ch:
					default:
					}
				}
				cancel()
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	if h.LastID() == 0 {
		t.Error("nothing was published")
	}
}
