// Package sse provides a simple single-user Server-Sent Events broker.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Event is one SSE message sent to the client.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker fans out events to all connected SSE clients.
// Single-user app: one or two browser tabs at most.
type Broker struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan Event]struct{})}
}

// Publish broadcasts an event to every subscriber.
func (b *Broker) Publish(eventType string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- Event{Type: eventType, Data: data}:
		default:
			// Drop if slow consumer — never block the caller.
		}
	}
}

// ServeHTTP implements http.Handler; keeps the connection open and streams events.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan Event, 64)
	b.subscribe(ch)
	defer b.unsubscribe(ch)

	// Send a heartbeat so the browser sees an open connection immediately.
	fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	for {
		select {
		case ev := <-ch:
			data, _ := json.Marshal(ev.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (b *Broker) subscribe(ch chan Event) {
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
}

func (b *Broker) unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subs, ch)
	close(ch)
	b.mu.Unlock()
}
