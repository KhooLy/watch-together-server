package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait    = 10 * time.Second
	readWait     = 90 * time.Second
	pingInterval = 30 * time.Second
	sendBuffer   = 64
)

var errClientGone = errors.New("client is closed")

type room struct {
	code      string
	hostID    string
	clients   map[string]*client
	state     playbackState
	sequence  int64
	updatedAt time.Time
}

type client struct {
	id        string
	name      string
	buffering bool
	conn      *websocket.Conn
	send      chan []byte
	sendMu    sync.Mutex
	closed    bool
	room      *room
}

func newClient(conn *websocket.Conn) *client {
	return &client{id: newClientID(), name: "Guest", conn: conn, send: make(chan []byte, sendBuffer)}
}

type hub struct {
	mu             sync.RWMutex
	rooms          map[string]*room
	maxRoomMembers int
	roomTTL        time.Duration
}

func newHub(maxRoomMembers int, roomTTL time.Duration) *hub {
	return &hub{rooms: make(map[string]*room), maxRoomMembers: maxRoomMembers, roomTTL: roomTTL}
}

type roomChange struct{ room *room }

func (h *hub) createRoom(c *client, name string) (*room, error) {
	h.mu.Lock()
	c.name = name
	previous := h.leaveLocked(c)
	code, err := h.uniqueCodeLocked()
	if err != nil {
		h.mu.Unlock()
		h.notifyRoomChange(previous)
		return nil, err
	}
	r := &room{code: code, hostID: c.id, clients: map[string]*client{c.id: c}, updatedAt: time.Now()}
	c.room = r
	h.rooms[code] = r
	h.mu.Unlock()
	h.notifyRoomChange(previous)
	return r, nil
}

func (h *hub) joinRoom(c *client, code string, name string) (*room, error) {
	code = normalizeRoomCode(code)
	if !validRoomCode(code) {
		return nil, errors.New("invalid room code")
	}

	h.mu.Lock()
	r := h.rooms[code]
	if r == nil {
		h.mu.Unlock()
		return nil, errors.New("room not found")
	}
	c.name = name
	if c.room == r {
		r.updatedAt = time.Now()
		h.mu.Unlock()
		return r, nil
	}
	if len(r.clients) >= h.maxRoomMembers {
		h.mu.Unlock()
		return nil, errors.New("room is full")
	}
	previous := h.leaveLocked(c)
	r.clients[c.id] = c
	c.room = r
	r.updatedAt = time.Now()
	h.mu.Unlock()
	h.notifyRoomChange(previous)
	return r, nil
}

func (h *hub) leave(c *client) {
	h.mu.Lock()
	changed := h.leaveLocked(c)
	h.mu.Unlock()
	h.notifyRoomChange(changed)
}

func (h *hub) leaveLocked(c *client) *roomChange {
	r := c.room
	if r == nil {
		return nil
	}
	delete(r.clients, c.id)
	c.room = nil
	c.buffering = false
	if len(r.clients) == 0 {
		delete(h.rooms, r.code)
		return nil
	}
	if r.hostID == c.id {
		ids := make([]string, 0, len(r.clients))
		for id := range r.clients {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		r.hostID = ids[0]
	}
	r.updatedAt = time.Now()
	return &roomChange{room: r}
}

func (h *hub) notifyRoomChange(change *roomChange) {
	if change == nil || change.room == nil {
		return
	}
	h.broadcastMembers(change.room)
	h.notifyHostBuffering(change.room)
}

func (h *hub) handleState(c *client, msg clientMessage) {
	h.mu.Lock()
	r := c.room
	if r == nil || r.hostID != c.id {
		h.mu.Unlock()
		return
	}
	r.state = playbackState{
		PositionMs: msg.PositionMs, DurationMs: msg.DurationMs, Playing: msg.Playing, Buffering: msg.Buffering,
		ContentID: msg.ContentID, ContentType: msg.ContentType, VideoID: msg.VideoID, Title: msg.Title,
	}
	r.sequence++
	sequence := r.sequence
	r.updatedAt = time.Now()
	clients := copyClients(r)
	h.mu.Unlock()

	payload := map[string]any{
		"type": msgSync, "sequence": sequence, "senderId": c.id, "serverTimeMs": time.Now().UnixMilli(),
		"positionMs": msg.PositionMs, "durationMs": msg.DurationMs, "playing": msg.Playing, "buffering": msg.Buffering,
		"contentId": msg.ContentID, "contentType": msg.ContentType, "title": msg.Title,
	}
	if msg.VideoID != "" {
		payload["videoId"] = msg.VideoID
	}
	broadcast(clients, payload)
}

func (h *hub) handleContent(c *client, msg clientMessage) {
	h.mu.Lock()
	r := c.room
	if r == nil || r.hostID != c.id {
		h.mu.Unlock()
		return
	}
	r.state.ContentID, r.state.ContentType, r.state.VideoID, r.state.Title = msg.ContentID, msg.ContentType, msg.VideoID, msg.Title
	r.updatedAt = time.Now()
	clients := copyClients(r)
	h.mu.Unlock()

	payload := map[string]any{"type": msgContent, "contentId": msg.ContentID, "contentType": msg.ContentType, "title": msg.Title}
	if msg.VideoID != "" {
		payload["videoId"] = msg.VideoID
	}
	broadcast(clients, payload)
}

func (h *hub) handleBuffering(c *client, buffering bool) {
	h.mu.Lock()
	r := c.room
	if r == nil {
		h.mu.Unlock()
		return
	}
	c.buffering = buffering
	r.updatedAt = time.Now()
	h.mu.Unlock()
	h.broadcastMembers(r)
	h.notifyHostBuffering(r)
}

func (h *hub) notifyHostBuffering(r *room) {
	h.mu.RLock()
	if h.rooms[r.code] != r {
		h.mu.RUnlock()
		return
	}
	host := r.clients[r.hostID]
	anyBuffering := false
	for id, member := range r.clients {
		if id != r.hostID && member.buffering {
			anyBuffering = true
			break
		}
	}
	h.mu.RUnlock()
	if host != nil {
		_ = host.writeJSON(map[string]any{"type": msgBuffering, "senderId": "server", "anyBuffering": anyBuffering})
	}
}

func (h *hub) roomPayload(c *client, r *room) map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return map[string]any{"type": msgRoom, "room": r.code, "clientId": c.id, "hostId": r.hostID, "members": memberViews(r)}
}

func (h *hub) broadcastMembers(r *room) {
	h.mu.RLock()
	if h.rooms[r.code] != r {
		h.mu.RUnlock()
		return
	}
	payload := map[string]any{"type": msgMembers, "hostId": r.hostID, "members": memberViews(r)}
	clients := copyClients(r)
	h.mu.RUnlock()
	broadcast(clients, payload)
}

func (h *hub) sendCurrentState(c *client, r *room) {
	h.mu.RLock()
	if h.rooms[r.code] != r {
		h.mu.RUnlock()
		return
	}
	state, sequence := r.state, r.sequence
	h.mu.RUnlock()

	if state.ContentID != "" {
		payload := map[string]any{"type": msgContent, "contentId": state.ContentID, "contentType": state.ContentType, "title": state.Title}
		if state.VideoID != "" {
			payload["videoId"] = state.VideoID
		}
		_ = c.writeJSON(payload)
	}
	if state.DurationMs > 0 || state.PositionMs > 0 || state.Playing {
		payload := map[string]any{
			"type": msgSync, "sequence": sequence, "senderId": "server", "serverTimeMs": time.Now().UnixMilli(),
			"positionMs": state.PositionMs, "durationMs": state.DurationMs, "playing": state.Playing, "buffering": state.Buffering,
			"contentId": state.ContentID, "contentType": state.ContentType, "title": state.Title,
		}
		if state.VideoID != "" {
			payload["videoId"] = state.VideoID
		}
		_ = c.writeJSON(payload)
	}
}

func (h *hub) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired := h.collectExpiredRooms(time.Now().Add(-h.roomTTL))
			for _, clients := range expired {
				for _, c := range clients {
					_ = c.writeJSON(map[string]any{"type": msgError, "message": "room expired"})
					c.close()
				}
			}
		}
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	clients := make([]*client, 0)
	for _, r := range h.rooms {
		for _, c := range r.clients {
			c.room = nil
			clients = append(clients, c)
		}
	}
	h.rooms = make(map[string]*room)
	h.mu.Unlock()
	for _, c := range clients {
		c.close()
	}
}

func (h *hub) collectExpiredRooms(cutoff time.Time) [][]*client {
	h.mu.Lock()
	defer h.mu.Unlock()
	expired := make([][]*client, 0)
	for code, r := range h.rooms {
		if !r.updatedAt.Before(cutoff) {
			continue
		}
		clients := copyClients(r)
		for _, c := range clients {
			c.room = nil
			c.buffering = false
		}
		delete(h.rooms, code)
		expired = append(expired, clients)
	}
	return expired
}

func (h *hub) uniqueCodeLocked() (string, error) {
	for i := 0; i < 32; i++ {
		code, err := randomCode(roomCodeLength)
		if err != nil {
			return "", err
		}
		if h.rooms[code] == nil {
			return code, nil
		}
	}
	return "", errors.New("could not allocate room code")
}

func memberViews(r *room) []memberView {
	ids := make([]string, 0, len(r.clients))
	for id := range r.clients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	members := make([]memberView, 0, len(ids))
	for _, id := range ids {
		c := r.clients[id]
		members = append(members, memberView{ID: c.id, Name: c.name, Buffering: c.buffering})
	}
	return members
}

func copyClients(r *room) []*client {
	out := make([]*client, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c)
	}
	return out
}

func broadcast(clients []*client, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range clients {
		_ = c.enqueue(data)
	}
}

func (c *client) writeJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.enqueue(data)
}

func (c *client) enqueue(data []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed {
		return errClientGone
	}
	select {
	case c.send <- data:
		return nil
	default:
		c.closeLocked()
		return errors.New("send buffer full")
	}
}

func (c *client) close() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.closeLocked()
}

func (c *client) closeLocked() {
	if c.closed {
		return
	}
	c.closed = true
	close(c.send)
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case data, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
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
