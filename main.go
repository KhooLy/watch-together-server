package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	h := newHub(config.maxMembers, time.Duration(config.roomTTLMinutes)*time.Minute)
	connections := newConnectionLimiter(config.maxConnections, config.maxConnectionsPerAddress)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go h.cleanupLoop(ctx)

	upgrader := websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		EnableCompression: false,
		CheckOrigin: func(r *http.Request) bool {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				return true
			}
			if _, allowed := config.allowedOrigins["*"]; allowed {
				return true
			}
			_, allowed := config.allowedOrigins[origin]
			return allowed
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if config.secret != "" && r.URL.Query().Get("token") != config.secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		address := remoteAddress(r, config.trustProxyHeaders)
		if !connections.acquire(address) {
			http.Error(w, "too many connections", http.StatusTooManyRequests)
			return
		}
		defer connections.release(address)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &client{id: newClientID(), name: "Guest", conn: conn}
		defer func() { h.leave(c); _ = conn.Close() }()
		messageLimiter := newTokenBucket(config.messagesPerSecond, config.messageBurst)
		violations := 0

		conn.SetReadLimit(64 * 1024)
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(90 * time.Second)) })

		for {
			var msg clientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			if !messageLimiter.allow() {
				violations++
				_ = c.writeJSON(map[string]any{"type": msgError, "message": "message rate limit exceeded"})
				if violations >= 3 {
					break
				}
				continue
			}
			if err := validateMessage(msg); err != nil {
				_ = c.writeJSON(map[string]any{"type": msgError, "message": err.Error()})
				continue
			}
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			switch strings.ToLower(strings.TrimSpace(msg.Type)) {
			case msgCreate:
				c.name = sanitizeName(msg.Name)
				r, err := h.createRoom(c)
				if err != nil {
					_ = c.writeJSON(map[string]any{"type": msgError, "message": err.Error()})
					continue
				}
				_ = c.writeJSON(h.roomPayload(c, r))
				h.broadcastMembers(r)
			case msgJoin:
				c.name = sanitizeName(msg.Name)
				r, err := h.joinRoom(c, msg.Room)
				if err != nil {
					_ = c.writeJSON(map[string]any{"type": msgError, "message": err.Error()})
					continue
				}
				_ = c.writeJSON(h.roomPayload(c, r))
				h.broadcastMembers(r)
				h.sendCurrentState(c, r)
			case msgLeave:
				h.leave(c)
			case msgState:
				h.handleState(c, msg)
			case msgContent:
				h.handleContent(c, msg)
			case msgBuffering:
				h.handleBuffering(c, msg.Buffering)
			case msgPing:
				_ = c.writeJSON(map[string]any{"type": msgPong, "serverTimeMs": time.Now().UnixMilli(), "clientTimeMs": msg.ClientTimeMs})
			default:
				_ = c.writeJSON(map[string]any{"type": msgError, "message": "unknown message type"})
			}
		}
	})

	server := &http.Server{
		Addr:              ":" + config.port,
		Handler:           mux,
		MaxHeaderBytes:    16 * 1024,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h.closeAll()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Watch Together server listening on :%s (max room members=%d, max connections=%d)", config.port, config.maxMembers, config.maxConnections)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
