# Watch Together Server

Self-hosted, open-source room and playback synchronization server for watch-together clients. It is application-agnostic: clients exchange room membership, content identity, playback state, buffering state, and clock pings over WebSocket. Video and audio never pass through this server.

## Run with Docker

Create a `.env` file next to `docker-compose.yml`:

```dotenv
WATCH_TOGETHER_SECRET=replace-with-a-long-random-token
WATCH_TOGETHER_ALLOWED_ORIGINS=https://your-client.example
TRUST_PROXY_HEADERS=true
```

Then start the server:

```bash
docker compose up -d --build
```

The server listens on port `8787` by default. The supplied Compose file binds it to loopback so Internet traffic must pass through the TLS reverse proxy.

Environment variables:

- `WATCH_TOGETHER_SECRET`: shared token required during the WebSocket handshake
- `ALLOW_ANONYMOUS`: set to `true` only for intentionally public, unauthenticated instances; default `false`
- `WATCH_TOGETHER_ALLOWED_ORIGINS`: comma-separated browser origins, for example `https://app.example.com,https://another.example`; native clients without an `Origin` header are allowed
- `TRUST_PROXY_HEADERS`: set to `true` only when a trusted reverse proxy overwrites `X-Forwarded-For` or `X-Real-IP`; default `false`
- `MAX_ROOM_MEMBERS`: maximum members per room, default `12`
- `ROOM_TTL_MINUTES`: inactive room lifetime, default `360`
- `PORT`: listen port, default `8787`
- `MAX_CONNECTIONS`: server-wide concurrent WebSocket limit, default `1000`
- `MAX_CONNECTIONS_PER_ADDRESS`: concurrent WebSocket limit per remote address, default `20`
- `MESSAGES_PER_SECOND`: per-connection sustained message limit, default `30`
- `MESSAGE_BURST`: per-connection burst limit, default `60`

Health check:

```text
GET /health
```

WebSocket endpoint:

```text
/ws
```

Clients connect with `?token=...`. The token is required unless `ALLOW_ANONYMOUS=true`. Because browser WebSocket APIs cannot set arbitrary request headers, deploy behind TLS and avoid logging query strings at the reverse proxy.

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

The server sends WebSocket ping frames every 30 seconds and drops connections that go 90 seconds without traffic. Browsers answer ping frames automatically; native clients must reply with a pong. A client that cannot keep up with broadcasts is disconnected rather than allowed to delay the rest of its room.

Clients remain responsible for media playback, authorization UI, content resolution, drift correction, and reconnect behavior.

## Security

Use TLS for Internet deployments, configure `WATCH_TOGETHER_SECRET`, and set `WATCH_TOGETHER_ALLOWED_ORIGINS` to the exact client origins you trust. The server rejects browser origins that are not listed, limits concurrent connections, limits message rates, caps WebSocket frames, and expires inactive rooms. The server is intentionally stateless beyond active in-memory rooms; restarting it removes all rooms. For multiple instances, use sticky routing or add a shared room store and broker.

## License

MIT. See `LICENSE`.
