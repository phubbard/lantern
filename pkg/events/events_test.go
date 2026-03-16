package events

import (
	"sync"
	"testing"
	"time"

	"github.com/phubbard/lantern/pkg/model"
)

func TestNewRingBuffer(t *testing.T) {
	rb := NewRingBuffer(10)
	if rb.Len() != 0 {
		t.Errorf("expected empty buffer, got %d", rb.Len())
	}
	if rb.capacity != 10 {
		t.Errorf("expected capacity 10, got %d", rb.capacity)
	}
}

func TestNewRingBuffer_ZeroCapacity(t *testing.T) {
	rb := NewRingBuffer(0)
	if rb.capacity != 1 {
		t.Errorf("expected capacity 1 for zero input, got %d", rb.capacity)
	}
}

func TestRingBuffer_Push_And_GetAll(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push(model.HostEvent{Type: model.EventDHCPDiscover, Detail: "1"})
	rb.Push(model.HostEvent{Type: model.EventDHCPOffer, Detail: "2"})

	events := rb.GetAll()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Detail != "1" || events[1].Detail != "2" {
		t.Error("events not in expected order")
	}
}

func TestRingBuffer_Wrap(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push(model.HostEvent{Detail: "1"})
	rb.Push(model.HostEvent{Detail: "2"})
	rb.Push(model.HostEvent{Detail: "3"})
	rb.Push(model.HostEvent{Detail: "4"}) // overwrites "1"

	if rb.Len() != 3 {
		t.Errorf("expected 3 events, got %d", rb.Len())
	}

	events := rb.GetAll()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Should be in chronological order: 2, 3, 4
	if events[0].Detail != "2" || events[1].Detail != "3" || events[2].Detail != "4" {
		t.Errorf("expected [2,3,4], got [%s,%s,%s]", events[0].Detail, events[1].Detail, events[2].Detail)
	}
}

func TestNewStore(t *testing.T) {
	store := NewStore(100)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.perHostLimit != 100 {
		t.Errorf("expected perHostLimit 100, got %d", store.perHostLimit)
	}
}

func TestNewStore_ZeroLimit(t *testing.T) {
	store := NewStore(0)
	if store.perHostLimit != 1000 {
		t.Errorf("expected default perHostLimit 1000, got %d", store.perHostLimit)
	}
}

func TestStore_Record_And_GetByMAC(t *testing.T) {
	store := NewStore(100)

	evt := model.HostEvent{
		Timestamp: time.Now(),
		MAC:       "aa:bb:cc:dd:ee:ff",
		IP:        "192.168.1.100",
		Type:      model.EventDHCPDiscover,
		Detail:    "test discover",
	}

	store.Record(evt)

	events := store.GetByMAC("aa:bb:cc:dd:ee:ff")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Detail != "test discover" {
		t.Errorf("expected detail 'test discover', got %q", events[0].Detail)
	}
}

func TestStore_GetByIP(t *testing.T) {
	store := NewStore(100)

	store.Record(model.HostEvent{
		IP:   "10.0.0.1",
		Type: model.EventDNSQuery,
	})

	events := store.GetByIP("10.0.0.1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestStore_GetByClientID(t *testing.T) {
	store := NewStore(100)

	store.Record(model.HostEvent{
		ClientID: "client-123",
		Type:     model.EventDHCPAck,
	})

	events := store.GetByClientID("client-123")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestStore_GetByMAC_NotFound(t *testing.T) {
	store := NewStore(100)
	events := store.GetByMAC("00:00:00:00:00:00")
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown MAC, got %d", len(events))
	}
}

func TestStore_GetByIP_NotFound(t *testing.T) {
	store := NewStore(100)
	events := store.GetByIP("1.2.3.4")
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown IP, got %d", len(events))
	}
}

func TestStore_GetByClientID_NotFound(t *testing.T) {
	store := NewStore(100)
	events := store.GetByClientID("nonexistent")
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown client ID, got %d", len(events))
	}
}

func TestStore_GetRecent(t *testing.T) {
	store := NewStore(100)

	for i := 0; i < 5; i++ {
		store.Record(model.HostEvent{
			MAC:    "aa:bb:cc:dd:ee:ff",
			Detail: string(rune('a' + i)),
		})
	}

	recent := store.GetRecent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent events, got %d", len(recent))
	}
}

func TestStore_GetRecent_MoreThanAvailable(t *testing.T) {
	store := NewStore(100)

	store.Record(model.HostEvent{Detail: "only"})

	recent := store.GetRecent(10)
	if len(recent) != 1 {
		t.Fatalf("expected 1 event, got %d", len(recent))
	}
}

func TestStore_Subscribe_Unsubscribe(t *testing.T) {
	store := NewStore(100)

	ch := store.Subscribe()
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	// Record an event and verify it's received
	go func() {
		time.Sleep(10 * time.Millisecond)
		store.Record(model.HostEvent{Detail: "subscriber-test"})
	}()

	select {
	case evt := <-ch:
		if evt.Detail != "subscriber-test" {
			t.Errorf("expected detail 'subscriber-test', got %q", evt.Detail)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for event on subscriber channel")
	}

	store.Unsubscribe(ch)
}

func TestStore_ConcurrentAccess(t *testing.T) {
	store := NewStore(50)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				store.Record(model.HostEvent{
					MAC:    "aa:bb:cc:dd:ee:ff",
					IP:     "192.168.1.1",
					Type:   model.EventDNSQuery,
					Detail: "concurrent",
				})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.GetByMAC("aa:bb:cc:dd:ee:ff")
			store.GetByIP("192.168.1.1")
			store.GetRecent(10)
		}()
	}

	wg.Wait()
}
