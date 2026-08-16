package main

import (
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
)

const roomAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const roomCodeLength = 6

const (
	msgCreate    = "create"
	msgJoin      = "join"
	msgLeave     = "leave"
	msgState     = "state"
	msgContent   = "content"
	msgBuffering = "buffering"
	msgPing      = "ping"
	msgPong      = "pong"
	msgRoom      = "room"
	msgMembers   = "members"
	msgSync      = "sync"
	msgError     = "error"
)

type clientMessage struct {
	Type         string `json:"type"`
	Name         string `json:"name,omitempty"`
	Room         string `json:"room,omitempty"`
	PositionMs   int64  `json:"positionMs,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
	Playing      bool   `json:"playing,omitempty"`
	Buffering    bool   `json:"buffering,omitempty"`
	ContentID    string `json:"contentId,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	VideoID      string `json:"videoId,omitempty"`
	Title        string `json:"title,omitempty"`
	ClientTimeMs int64  `json:"clientTimeMs,omitempty"`
}

type memberView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Buffering bool   `json:"buffering"`
}

type playbackState struct {
	PositionMs  int64
	DurationMs  int64
	Playing     bool
	Buffering   bool
	ContentID   string
	ContentType string
	VideoID     string
	Title       string
}

func normalizeRoomCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func validRoomCode(value string) bool {
	if len(value) != roomCodeLength {
		return false
	}
	for _, ch := range value {
		if !strings.ContainsRune(roomAlphabet, ch) {
			return false
		}
	}
	return true
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Guest"
	}
	runes := []rune(name)
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

func randomCode(length int) (string, error) {
	out := make([]byte, length)
	max := byte(255 - (256 % len(roomAlphabet)))
	for index := range out {
		for {
			raw := []byte{0}
			if _, err := rand.Read(raw); err != nil {
				return "", err
			}
			if raw[0] > max {
				continue
			}
			out[index] = roomAlphabet[int(raw[0])%len(roomAlphabet)]
			break
		}
	}
	return string(out), nil
}

func validateMessage(msg clientMessage) error {
	for _, value := range []struct {
		name  string
		value string
		limit int
	}{
		{"name", msg.Name, 128},
		{"room", msg.Room, roomCodeLength},
		{"contentId", msg.ContentID, 512},
		{"contentType", msg.ContentType, 64},
		{"videoId", msg.VideoID, 512},
		{"title", msg.Title, 512},
	} {
		if len([]rune(value.value)) > value.limit {
			return errors.New(value.name + " is too long")
		}
	}
	if msg.PositionMs < 0 || msg.DurationMs < 0 {
		return errors.New("playback positions cannot be negative")
	}
	return nil
}

var clientCounter uint64

func newClientID() string {
	n := atomic.AddUint64(&clientCounter, 1)
	suffix, _ := randomCode(4)
	return strconv.FormatUint(n, 36) + suffix
}
