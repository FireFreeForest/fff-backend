package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"firefreeforest/backend/internal/observability"
)

type Hub struct {
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	done       chan struct{}
	log        *slog.Logger
	closeOnce  sync.Once
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 512),
		done:       make(chan struct{}),
		log:        log,
	}
}

func (h *Hub) Run(ctx context.Context) {
	defer h.closeOnce.Do(func() { close(h.done) })
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				close(c.send)
				_ = c.conn.Close()
				observability.WSConnections.Dec()
			}
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
			observability.WSConnections.Inc()
			h.log.Info("websocket client connected", "remote", c.conn.RemoteAddr().String(), "filter", c.filter)
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				observability.WSConnections.Dec()
				h.log.Info("websocket client disconnected", "remote", c.conn.RemoteAddr().String())
			}
		case msg := <-h.broadcast:
			h.dispatch(msg)
		}
	}
}

func (h *Hub) dispatch(msg []byte) {
	var env struct {
		DeviceID string `json:"device_id"`
	}
	_ = json.Unmarshal(msg, &env)
	for c := range h.clients {
		if c.filter != "" && env.DeviceID != "" && c.filter != env.DeviceID {
			continue
		}
		select {
		case c.send <- msg:
		default:
			h.log.Warn("dropping websocket message for slow client", "remote", c.conn.RemoteAddr().String())
		}
	}
}

func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	case <-h.done:
	default:
		h.log.Warn("websocket broadcast buffer full, dropping message")
	}
}

func (h *Hub) Serve(conn *websocket.Conn, filter string) {
	c := &client{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 64),
		filter: filter,
	}
	select {
	case h.register <- c:
	case <-h.done:
		_ = conn.Close()
		return
	}
	go c.writePump()
	go c.readPump()
}

type client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	filter string
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

func (c *client) readPump() {
	defer func() {
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
		}
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	welcome := []byte(`{"type":"connected"}`)
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := c.conn.WriteMessage(websocket.TextMessage, welcome); err != nil {
		return
	}

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
