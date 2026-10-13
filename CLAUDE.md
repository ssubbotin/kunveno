# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

kunveno is a self-hosted meeting service for a private network: voice, cameras and one shared
screen per room, rooms with a PIN, a Go SFU server and a Svelte client, installed from a Docker
Compose directory (`deploy/kunveno`, published as `kunveno.tgz` with every release). The README is
the user-facing documentation (English, with Russian and Spanish copies that must be kept in step);
`docs/deploy.md` is the operations guide. Working notes, design drafts and plans stay out of the
repository (they are excluded by the global git ignore on the workstation); the code and these
files are the source of truth.

## Commands

Everything builds and tests on this laptop through the Makefile. Never copy sources to any ssh host
(including `compute-host`). Keep runs foreground and stop dev servers as soon as a check is done.

```bash
make test        # test-go (go vet + go test -race -count=1) and test-web (vitest --run)
make test-go
make test-web
make build       # CGO_ENABLED=0 bin/kunveno-server, plus web/dist via npm run build
make deb         # nfpm: dist/kunveno-server_*.deb and dist/kunveno-web_*.deb
make deploy-bundle  # dist/kunveno.tgz: the stack directory deploy/kunveno with the version stamped in
make fmt         # gofmt + go mod tidy

go test ./internal/sfu -run TestNegotiateCoalesces -v    # single Go test
cd web && npx vitest run src/lib/protocol.test.ts        # single web test file
cd web && npm run check                                  # svelte-check (run in CI)
cd web && npm run dev                                    # Vite proxies /ws and /api to 127.0.0.1:8080
go run ./tools/loadgen -url wss://host/ws -room ops -pin 1234 -n 14 -duration 10m [-insecure]
```

Local smoke run: copy `deploy/kunveno/config.yaml`, set `advertise_ip` to this laptop's LAN address
(`ip -4 route get 1.1.1.1`), start `bin/kunveno-server -config <file>`, then `curl localhost:8080/healthz`,
`/api/rooms`, and `go run ./tools/loadgen -n 14 -duration 30s` (expect `tracks=182`). With
`advertise_ip: 127.0.0.1` HTTP works but no media connects: browsers and pion clients gather no
loopback candidates, so ICE never reaches the server's only candidate.

## Architecture

Three deployables on one Ubuntu host (24.04/26.04):

1. `kunveno-server` (Go 1.26, module `github.com/ssubbotin/kunveno`, pion/webrtc v4.2.x, no cgo):
   HTTP on `127.0.0.1:8080`, WebSocket signaling at `/ws`, and an SFU on one UDP port (default 7000).
2. `web/`: Svelte 5 + Vite + TypeScript static bundle, served by nginx from `/var/www/kunveno`.
   It is never embedded in the Go binary.
3. nginx: TLS, static files, reverse proxy of `/ws` and `/api/` to the Go server.

Go packages are separated so each is tested alone:

- `internal/room`: pure state machine (participants, mute, share lock, capacity, PIN), no I/O.
- `internal/sfu`: pion engine (codecs, interceptors, ICE lite, UDP mux) and one `Peer` per
  participant. The server is the only SDP offerer; `Negotiate` coalesces concurrent
  subscribe/unsubscribe so at most one offer is outstanding per peer.
- `internal/signal`: WebSocket sessions, message validation, snapshot fan-out, and coordination
  between `room` and media. It depends on the `Media`/`MediaPeer` interfaces, so handler tests use a
  fake media layer; `cmd/kunveno-server` adapts the real `sfu.Engine` to it.
- `internal/httpapi`: `GET /api/rooms` (never returns PINs), `GET /healthz`.
- `internal/config`: YAML at `/etc/kunveno/config.yaml`, with `KUNVENO_LISTEN` and
  `KUNVENO_ADVERTISE_IP` filling gaps; configured rooms need a restart to change, rooms created in
  the lobby are kept in `rooms_file`.

Media model, which explains most design choices:

- Each client has one `RTCPeerConnection` with three fixed send slots negotiated at join: mid 0
  audio, mid 1 screen, mid 2 camera (`sfu.kindForMid`; the server tells screen from camera by the
  slot, both are VP8). The client never renegotiates; starting/stopping a share or the camera is
  `replaceTrack` on that slot's sender, so there is no offer glare.
- Forwarded tracks use stream id = publisher's participant id (`<id>/camera` for a camera) and
  track id `audio`, `screen` or `camera`; the client identifies them from `event.streams[0].id`
  (`splitStream`) and `event.track.kind`. Cameras are 640x360 at 15 fps, capped at 350 kbit/s, with
  no simulcast; a participant can decline other cameras (`{"type":"video","receive":false}`).
- Share lock is enforced server side; the client attaches the screen track only after seeing itself
  as `sharer` in a `participants` snapshot. A disconnect releases the lock and unsubscribes everyone.
- Keyframes: PLI to the sharer whenever a subscriber attaches to the screen track; subscriber PLIs
  are forwarded to the sharer.
- Reconnection is always a fresh `join` (the client keeps name and PIN in `sessionStorage`).

Web client logic lives in `web/src/lib/` (`protocol.ts` validating parser, `state.svelte.ts` rune
state, `signaling.ts` reconnect with 1 to 10 s backoff, `rtc.ts`, `levels.ts`, `session.ts`,
`link.ts`, `pins.ts`). The pure modules have unit tests (vitest); browser behaviour is covered by the
manual checklist in `docs/test-matrix.md`. Rune state must stay in files named `*.svelte.ts`.

## Hard constraints

- No STUN, TURN, CDN or any external runtime dependency: clients use `iceServers: []`, the server
  advertises exactly one host candidate (`media.advertise_ip`). SFU tests need `IncludeLoopback: true`.
- Codecs are fixed: Opus PT 111 (`minptime=10;useinbandfec=1`) and VP8 PT 96 with nack, pli, fir,
  goog-remb. Register nothing else.
- Limits: capacity default 15, range 2..50; PIN 4..16 chars; name 1..32 chars after trim; slug
  `^[a-z0-9-]{1,32}$`. PIN comparison is constant time.
- The first frame on a socket must be a valid `join`; anything else gets `bad_message`, a closed
  socket and no leftover room state.
- pion stays on v4.2.x (patch upgrades only).
- Commits: English, conventional prefixes (`feat:`, `fix:`, `test:`, `chore:`, `docs:`), identity
  from the repo's git config (personal: `Sergey Subbotin <ssubbotin@gmail.com>`).
