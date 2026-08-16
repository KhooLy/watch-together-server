# Watch Together Server

Self-hosted, open-source room and playback synchronization server for watch-together clients. It is application-agnostic: clients exchange room membership, content identity, playback state, buffering state, and clock pings over WebSocket. Video and audio never pass through this server.

## Run with Docker

```bash
docker compose up -d --build
```

The server listens on port `8787` by default.

Environment variables:

- `WATCH_TOGETHER_SECRET`: optional shared token required during the WebSocket handshake
- `MAX_ROOM_MEMBERS`: maximum members per room, default `12`
- `ROOM_TTL_MINUTES`: inactive room lifetime, default `360`
- `PORT`: listen port, default `8787`

Health check:

```text
GET /health
```

WebSocket endpoint:

```text
/ws
```

When `WATCH_TOGETHER_SECRET` is configured, clients connect with `?token=...`.

## Internet deployment

Put the server behind TLS using Caddy, Nginx, or Traefik. Browsers require secure WebSockets when the client is loaded over HTTPS.

Example Caddy configuration:

```text
watch.example.com {
    reverse_proxy 127.0.0.1:8787
}
```

Clients then connect to:

```text
wss://watch.example.com/ws
```

## Protocol

The protocol is JSON over WebSocket.

Client messages:

- `create`: create a room with `name`
- `join`: join a room with `room` and `name`
- `leave`: leave the current room
- `state`: host playback state
- `content`: host content identity
- `buffering`: member buffering state
- `ping`: client clock synchronization

Server messages:

- `room`: assigned room, client ID, host ID, and members
- `members`: current membership and host
- `sync`: host playback state with sequence and server timestamp
- `content`: current content identity
- `buffering`: aggregate buffering notification
- `pong`: server clock response
- `error`: request or room failure

Clients remain responsible for media playback, authorization UI, content resolution, drift correction, and reconnect behavior.

## Security

Use TLS for Internet deployments and configure `WATCH_TOGETHER_SECRET`. The server is intentionally stateless beyond active in-memory rooms; restarting it removes all rooms. Add network-level rate limiting or an authenticated reverse proxy for public deployments.

## License

MIT. See `LICENSE`.
