# kunveno

Self-hosted video meetings for a company network: voice, cameras and screen sharing in the
browser, with a small Go server you run yourself. Esperanto: "meeting".

Built for an office or VPN where you want calls without third-party services: no accounts, no
STUN or TURN servers, no CDN, no analytics. A room is a name and a PIN; everything runs on one
host behind nginx.

Private repository, internal use.

## Features

- Rooms with a PIN, defined in the configuration or created from the lobby (persisted on the
  host). Up to 50 people per room (15 by default).
- Audio from everyone, cameras (640x360, 15 fps), one shared screen at a time.
- A pre-join check: microphone and camera choice, a level meter, and entering with the
  microphone or camera off. Works on computers without a camera.
- A connection-quality indicator on every participant (loss, round trip and jitter, measured
  on the server).
- An echo sound check per room, and rooms that play a video file in a loop.
- Works behind awkward networks: when a participant's UDP path fails, the browser falls back to
  ICE over TCP on the same port.
- Interface in Russian (default), English and Spanish; the installation title is configurable.
- Protection against PIN guessing, Prometheus metrics with a Grafana dashboard, logs in the host
  journal.

## How it works

```
browser ──HTTPS/WSS──▶ nginx ──▶ kunveno-server  (signaling, SFU, HTTP API)
browser ◀──WebRTC (UDP 7000, or TCP 7000)──▶ kunveno-server
                         kunveno-echo, kunveno-player ──loopback──▶ kunveno-server
```

- `kunveno-server` (Go, [pion/webrtc](https://github.com/pion/webrtc)) is a selective forwarding
  unit: each participant sends one stream per kind (audio, screen, camera) and the server forwards
  it to everyone else. ICE runs in lite mode with a single host candidate (`media.advertise_ip`),
  one UDP port for all media and the same port on TCP as a fallback.
- Each browser keeps one `RTCPeerConnection` with three fixed send slots (audio, screen, camera);
  turning a camera or share on or off swaps the track without renegotiation. The server is the
  only SDP offerer.
- The web client (Svelte 5, Vite, TypeScript) is a static bundle served by nginx, which also
  terminates TLS and proxies `/ws` and `/api/` to the server on loopback.
- `kunveno-echo` and `kunveno-player` are helpers that join rooms as participants over loopback:
  the echo plays a room's sound back with a delay when someone asks for it; the player plays a
  configured video file in a loop.

[deployment guide](docs/deploy.md).

## Repository layout

| Path | What |
|---|---|
| `cmd/kunveno-server` | server process: configuration, HTTP, signaling, media |
| `cmd/kunveno-echo`, `cmd/kunveno-player` | the echo and video-loop helpers |
| `internal/room`, `internal/signal`, `internal/sfu` | room state, WebSocket signaling, the SFU |
| `internal/config`, `internal/httpapi`, `internal/metrics` | configuration, HTTP API, Prometheus metrics |
| `internal/quality`, `internal/ratelimit`, `internal/roomstore` | connection grades, PIN limits, saved rooms |
| `internal/echo`, `internal/player`, `internal/botclient` | helper internals |
| `web/` | the browser client |
| `deploy/` | Docker images and compose stack, nginx, systemd unit, deb packaging, example configuration |
| `tools/loadgen` | synthetic participants for load and end-to-end checks |

## Development

Requirements: Go 1.26, Node 22, and ffmpeg for the player's tests (skipped without it).

```bash
make test        # go vet + go test -race, and the web client's tests
make build       # bin/kunveno-server and web/dist
make fmt         # gofmt + go mod tidy

go test ./internal/sfu -run TestCameraFlowsBetweenTwoPeers -v    # one Go test
cd web && npx vitest run src/lib/pins.test.ts                    # one web test file
cd web && npm run check                                          # svelte-check
```

Running locally: copy `deploy/config.example.yaml`, set `media.advertise_ip` to the machine's LAN
address (browsers gather no loopback candidates, so `127.0.0.1` gives no media), start
`bin/kunveno-server -config <file>`, then `cd web && npm run dev` (Vite proxies `/ws` and `/api/`
to `127.0.0.1:8080`). `go run ./tools/loadgen -n 14 -duration 30s` adds synthetic participants.

## Deployment

CI (`.github/workflows/images.yml`) builds four images, checks that none of them carries sources,
source maps or debug data, and publishes them to GHCR (private):
`ghcr.io/ssubbotin/kunveno-{server,web,echo,player}`. A tag `vX.Y.Z` publishes `X.Y.Z` and
`latest`, `main` publishes `main`, and a pull request from this repository publishes a preview
`pr-<number>`.

On the host, the compose stack in `deploy/docker` runs those images next to Prometheus and Grafana:

```bash
make deploy-bundle                                   # compose file and configuration templates only
rsync -a dist/kunveno-docker/ <host>:kunveno/        # on later updates keep config.yaml, .env,
                                                     # certs/, data/ and media/ out of --delete
# on the host, in ~/kunveno:
cp config.example.yaml config.yaml && cp env.example .env   # then edit both
docker login ghcr.io                                 # once, with a read:packages token
docker compose pull && docker compose up -d
```

Upgrading is `KUNVENO_VERSION=<release>` in `.env` followed by
`docker compose pull && docker compose up -d`. The full guide, including TLS, the firewall
(TCP 443, UDP and TCP 7000), Debian packages and troubleshooting, is in [docs/deploy.md](docs/deploy.md).

## Configuration

`config.yaml` (read at start; the server refuses unknown keys):

| Key | Meaning |
|---|---|
| `http.listen` | address nginx proxies to, on loopback |
| `media.advertise_ip` | the address browsers send media to (the host's VPN or LAN address) |
| `media.udp_port`, `media.tcp_port` | media ports (7000 by default; `tcp_port: -1` turns the TCP fallback off) |
| `media.share_max_kbps` | bitrate cap for screen sharing |
| `ui.title` | title shown in the lobby and the browser tab |
| `ui.reactions` | emoji participants can send as reactions (1..16; ten by default) |
| `rooms[]` | `slug`, `name`, `pin`, `max_participants`, and optionally `player: {file, name}` for a video loop from the media directory |
| `rooms_file` | where rooms created in the lobby are saved (`/var/lib/kunveno/rooms.json`) |

`.env` next to the compose file sets the release (`KUNVENO_VERSION`), ports, the public host name
for Grafana and its admin password.

## Operations

- Logs: `journalctl -t kunveno-server --since "7 days ago" -o cat` (also `kunveno-web`,
  `kunveno-echo`, `kunveno-player`, `kunveno-prometheus`, `kunveno-grafana`). Each connection logs
  its media path (UDP or TCP), quality changes and wrong PINs.
- Metrics: `/metrics` on the server (loopback only); Grafana at `https://<host>/grafana/` with the
  provisioned "kunveno" dashboard.
- Data to keep on the host: `config.yaml`, `.env`, `certs/`, `data/` (rooms created in the lobby,
  the player's cache) and `media/`.

## Versions

Releases are git tags `vX.Y.Z` on merge commits to `main`; the version shows in the lobby footer
and in the `kunveno_build_info` metric.
