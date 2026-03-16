package events

import (
	"sync"

	"github.com/phubbard/lantern/pkg/model"
)

// RingBuffer is a fixed-size circular buffer for HostEvents.
type RingBuffer struct {
	mu       sync.RWMutex
	events   []model.HostEvent
	capacity int
	index    int // next write position
	full     bool
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &RingBuffer{
		events:   make([]model.HostEvent, capacity),
		capacity: capacity,
		index:    0,
		full:     false,
	}
}

// Push adds an event to the ring buffer, overwriting the oldest if full.
func (rb *RingBuffer) Push(event model.HostEvent) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.events[rb.index] = event
	rb.index = (rb.index + 1) % rb.capacity

	if rb.index == 0 {
		rb.full = true
	}
}

// GetAll returns all events in chronological order (oldest to newest).
func (rb *RingBuffer) GetAll() []model.HostEvent {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]model.HostEvent, 0, rb.capacity)

	if !rb.full {
		// Buffer not full, return from start to current index
		return append(result, rb.events[:rb.index]...)
	}

	// Buffer is full, return from index (oldest) to end, then from start to index
	result = append(result, rb.events[rb.index:]...)
	result = append(result, rb.events[:rb.index]...)
	return result
}

// Len returns the number of events in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.full {
		return rb.capacity
	}
	return rb.index
}

// Store manages per-host event ring buffers indexed by MAC, IP, and ClientID.
type Store struct {
	mu sync.RWMutex

	// Maps of ring buffers indexed by identifier
	byMAC      map[string]*RingBuffer
	byIP       map[string]*RingBuffer
	byClientID map[string]*RingBuffer

	// Global recent events buffer
	recent *RingBuffer

	// Per-host limit
	perHostLimit int

	// Subscribers for SSE streaming
	subscribers map[chan model.HostEvent]struct{}
	subMu       sync.RWMutex
}

// NewStore creates a new event store with the given per-host limit.
func NewStore(perHostLimit int) *Store {
	if perHostLimit <= 0 {
		perHostLimit = 1000
	}
	return &Store{
		byMAC:       make(map[string]*RingBuffer),
		byIP:        make(map[string]*RingBuffer),
		byClientID:  make(map[string]*RingBuffer),
		recent:      NewRingBuffer(perHostLimit * 10), // global buffer is larger
		perHostLimit: perHostLimit,
		subscribers: make(map[chan model.HostEvent]struct{}),
	}
}

// Record adds an event to all relevant ring buffers (by MAC, IP, clientID).
// Thread-safe.
func (s *Store) Record(event model.HostEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Add to global recent buffer
	s.recent.Push(event)

	// Add to MAC-indexed buffer
	if event.MAC != "" {
		if _, exists := s.byMAC[event.MAC]; !exists {
			s.byMAC[event.MAC] = NewRingBuffer(s.perHostLimit)
		}
		s.byMAC[event.MAC].Push(event)
	}

	// Add to IP-indexed buffer
	if event.IP != "" {
		if _, exists := s.byIP[event.IP]; !exists {
			s.byIP[event.IP] = NewRingBuffer(s.perHostLimit)
		}
		s.byIP[event.IP].Push(event)
	}

	// Add to ClientID-indexed buffer
	if event.ClientID != "" {
		if _, exists := s.byClientID[event.ClientID]; !exists {
			s.byClientID[event.ClientID] = NewRingBuffer(s.perHostLimit)
		}
		s.byClientID[event.ClientID].Push(event)
	}

	// Notify subscribers
	s.notifySubscribers(event)
}

// GetByMAC returns all events for a given MAC address.
func (s *Store) GetByMAC(mac string) []model.HostEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rb, exists := s.byMAC[mac]
	if !exists {
		return []model.HostEvent{}
	}

	return rb.GetAll()
}

// GetByIP returns all events for a given IP address.
func (s *Store) GetByIP(ip string) []model.HostEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rb, exists := s.byIP[ip]
	if !exists {
		return []model.HostEvent{}
	}

	return rb.GetAll()
}

// GetByClientID returns all events for a given client ID.
func (s *Store) GetByClientID(id string) []model.HostEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rb, exists := s.byClientID[id]
	if !exists {
		return []model.HostEvent{}
	}

	return rb.GetAll()
}

// GetRecent returns the most recent n global events.
func (s *Store) GetRecent(n int) []model.HostEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.recent.GetAll()

	// Return the last n events
	if len(all) <= n {
		return all
	}

	return all[len(all)-n:]
}

// Subscribe creates a new subscriber channel for event notifications.
// Caller is responsible for draining the channel or closing it.
func (s *Store) Subscribe() chan model.HostEvent {
	ch := make(chan model.HostEvent, 100) // buffered channel to avoid blocking

	s.subMu.Lock()
	defer s.subMu.Unlock()

	s.subscribers[ch] = struct{}{}

	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *Store) Unsubscribe(ch chan model.HostEvent) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	delete(s.subscribers, ch)
	close(ch)
}

// notifySubscribers sends an event to all active subscribers.
// Must be called with s.mu held.
func (s *Store) notifySubscribers(event model.HostEvent) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()

	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
			// Channel full, skip to avoid blocking
		}
	}
}
