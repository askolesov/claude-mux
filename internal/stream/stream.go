// Package stream pushes state as it changes: a whole snapshot on every
// change, as server-sent events (a backend's, which the hub follows) or over
// a WebSocket (the hub's, which every window of the page holds), and JSON
// lines for the progress of a long request.
package stream

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

// The default CheckOrigin rejects cross-origin upgrades, so another site
// open in the browser can't follow the state.
var upgrader = websocket.Upgrader{}

// WebSocket streams snapshot() over a WebSocket, as JSON text messages:
// once now, then on every Notify, with a ping every 20 s to keep it alive.
// A browser holds at most six plain HTTP connections to one server, which
// server-sent events count against and WebSockets don't: every window of
// the page holds one of these.
func WebSocket(c *gin.Context, n *Notifier, snapshot func() any) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade has answered
	}
	defer func() { _ = ws.Close() }()
	sub := n.subscribe()
	defer n.unsubscribe(sub)
	// The page sends nothing; reading notices that it went away.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for err := ws.WriteJSON(snapshot()); err == nil; {
		select {
		case <-gone:
			return
		case <-sub:
			err = ws.WriteJSON(snapshot())
		case <-ping.C:
			err = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
	}
}

// Lines answers a request with JSON lines, each flushed as it is sent:
// progress first, then {"done": true} or {"error": …}.
type Lines struct {
	mu sync.Mutex
	c  *gin.Context
}

// NewLines starts the answer: 200, and JSON lines from here on.
func NewLines(c *gin.Context) *Lines {
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)
	return &Lines{c: c}
}

// Send sends v as one line. A page that went away misses it; the work it
// reports on goes on regardless.
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
