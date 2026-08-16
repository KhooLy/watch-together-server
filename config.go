package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type serverConfig struct {
	port                     string
	secret                   string
	allowAnonymous           bool
	trustProxyHeaders        bool
	allowedOrigins           map[string]struct{}
	maxMembers               int
	roomTTLMinutes           int
	maxConnections           int
	maxConnectionsPerAddress int
	messagesPerSecond        int
	messageBurst             int
}

func loadConfig() (serverConfig, error) {
	config := serverConfig{
		port:                     envString("PORT", "8787"),
		secret:                   strings.TrimSpace(os.Getenv("WATCH_TOGETHER_SECRET")),
		allowAnonymous:           envBool("ALLOW_ANONYMOUS", false),
		trustProxyHeaders:        envBool("TRUST_PROXY_HEADERS", false),
		allowedOrigins:           envList("WATCH_TOGETHER_ALLOWED_ORIGINS"),
		maxMembers:               envInt("MAX_ROOM_MEMBERS", 12),
		roomTTLMinutes:           envInt("ROOM_TTL_MINUTES", 360),
		maxConnections:           envInt("MAX_CONNECTIONS", 1000),
		maxConnectionsPerAddress: envInt("MAX_CONNECTIONS_PER_ADDRESS", 20),
		messagesPerSecond:        envInt("MESSAGES_PER_SECOND", 30),
		messageBurst:             envInt("MESSAGE_BURST", 60),
	}
	if config.secret == "" && !config.allowAnonymous {
		return serverConfig{}, errors.New("WATCH_TOGETHER_SECRET must be set unless ALLOW_ANONYMOUS=true")
	}
	if config.messageBurst < config.messagesPerSecond {
		config.messageBurst = config.messagesPerSecond
	}
	return config, nil
}

func envString(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return value
}

func envList(name string) map[string]struct{} {
	values := make(map[string]struct{})
	for _, value := range strings.Split(os.Getenv(name), ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = struct{}{}
		}
	}
	return values
}
