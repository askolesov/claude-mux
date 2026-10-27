// Package stream pushes state to the page as it changes: server-sent events
// of a whole snapshot on every change, and JSON lines for the progress of a
// long request. The hub and every backend serve both.
package stream

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Notifier wakes the event streams when the state they show changed.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (n *Notifier) subscribe() chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.subs == nil {
		n.subs = map[chan struct{}]struct{}{}
	}
	c := make(chan struct{}, 1)
	n.subs[c] = struct{}{}
	return c
}

func (n *Notifier) unsubscribe(c chan struct{}) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.subs, c)
}

// Notify wakes every stream; a wake-up already pending covers this one.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// Events streams snapshot() as server-sent events: once now, then on every
// Notify, and every 20 s to keep the connection alive.
func Events(c *gin.Context, n *Notifier, snapshot func() any) {
	sub := n.subscribe()
	defer n.unsubscribe(sub)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		data, _ := json.Marshal(snapshot())
		if _, err := c.Writer.Write(append(append([]byte("data: "), data...), '\n', '\n')); err != nil {
			return
		}
		c.Writer.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-sub:
		case <-ping.C:
		}
	}
}

// Lines answers a request with JSON lines, each flushed as it is sent:
// progress first, then {"done": true} or {"error": …}.
type Lines struct {
	mu sync.Mutex
	c  *gin.Context
}

func NewLines(c *gin.Context) *Lines {
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)
	return &Lines{c: c}
}

func (l *Lines) Send(v any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = json.NewEncoder(l.c.Writer).Encode(v)
	l.c.Writer.Flush()
}

// End sends how the request ended.
func (l *Lines) End(err error) {
	if err != nil {
		l.Send(gin.H{"error": err.Error()})
		return
	}
	l.Send(gin.H{"done": true})
}

// Write sends output as it comes, as {"out": …}.
func (l *Lines) Write(p []byte) (int, error) {
	l.Send(gin.H{"out": string(p)})
	return len(p), nil
}
