package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type connectionLimiter struct {
	mu             sync.Mutex
	total          int
	byAddress      map[string]int
	maxTotal       int
	maxPerAddress  int
}

func newConnectionLimiter(maxTotal int, maxPerAddress int) *connectionLimiter {
	return &connectionLimiter{
		byAddress:     make(map[string]int),
		maxTotal:      maxTotal,
		maxPerAddress: maxPerAddress,
	}
}

func (l *connectionLimiter) acquire(address string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || l.byAddress[address] >= l.maxPerAddress {
		return false
	}
	l.total++
	l.byAddress[address]++
	return true
}

func (l *connectionLimiter) release(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total > 0 {
		l.total--
	}
	if count := l.byAddress[address] - 1; count > 0 {
		l.byAddress[address] = count
	} else {
		delete(l.byAddress, address)
	}
}

type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	rate   float64
	burst  float64
}

func newTokenBucket(rate int, burst int) *tokenBucket {
	now := time.Now()
	return &tokenBucket{tokens: float64(burst), last: now, rate: float64(rate), burst: float64(burst)}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = minFloat(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func minFloat(left float64, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func remoteAddress(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" && net.ParseIP(forwarded) != nil {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" && net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
