# kunveno

English · [Русский](README.ru.md) · [Español](README.es.md)

Self-hosted video meetings for a company network: voice, cameras and screen sharing in the
browser, with a small Go server you run yourself. Esperanto: "meeting".

Built for an office or VPN where you want calls without third-party services: no accounts, no
STUN or TURN servers, no CDN, no analytics. A room is a name and a PIN; everything runs on one
host behind nginx.

Licensed under the GNU Affero General Public License v3.0 only; a commercial license is available
(see [License](#license)).

![A room with six participants, cameras on](docs/images/room.png)

## Features

- Rooms with a PIN, defined in the configuration or created from the lobby (persisted on the
  host). Up to 50 people per room (15 by default).
- Audio from everyone, cameras (640x360, 15 fps), one shared screen at a time.
- A pre-join check: microphone and camera choice, a level meter, and entering with the
  microphone or camera off. Works on computers without a camera.
- Virtual backgrounds, processed in the browser: blur, glass blocks, images the administrator puts
  in `backgrounds/`, or your own image that never leaves the browser. Noise suppression for the
  microphone (keyboard clicks, background sounds), also in the browser.
- Links to a room (`https://<host>/join/<room>#pin=…`), copied from the lobby or from inside a
  room: the recipient lands in the pre-join check with the room and PIN filled in. The PIN sits
  in the fragment, which the browser keeps to itself: it never reaches the server or its logs.
- A connection-quality indicator on every participant (loss, round trip and jitter, measured
  on the server).
- An echo sound check per room, rooms that play a video file in a loop, and rooms that show a
  live RTSP camera.
- Meeting notes with the commercial `kunveno-notes` add-on: each participant's speech is recognised
  on the server, and the text of a room's last meeting is downloaded as Markdown (see
  [Meeting notes](#meeting-notes)).
- Meeting recording with the commercial `kunveno-recorder` add-on: the shared screen and everyone's
  audio as one MP4 video, downloaded from the room or the lobby (see
  [Meeting recording](#meeting-recording)).
- Works behind awkward networks: when a participant's UDP path fails, the browser falls back to
  ICE over TCP on the same port.
- Interface in Russian (default), English and Spanish; the installation title is configurable.
- Protection against PIN guessing, Prometheus metrics with a Grafana dashboard, logs in the host
  journal.

![The lobby: rooms and the pre-join check](docs/images/lobby.png)

## Setup

You need a Linux server with Docker that your users can reach over the network.

**1. Download and unpack** the stack:

```bash
curl -fsSL https://github.com/ssubbotin/kunveno/releases/latest/download/kunveno.tgz | tar xz
cd kunveno
```

The directory is the whole installation:

```
docker-compose.yml
.env            settings: fill in two lines, the rest is optional
config.yaml     rooms, PINs and the title; the defaults work as they are
data/           rooms created in the lobby, the player's cache
certs/          TLS certificate: self-signed on first start, or your own
backgrounds/    images offered as virtual backgrounds
media/          video files for rooms with a player
grafana/        Grafana's data source and dashboard
```

**2. Fill in two lines in `.env`:**

```bash
PUBLIC_HOST=10.0.0.5            # the server's address or name, as users type it in the browser
GRAFANA_ADMIN_PASSWORD=...      # any strong password; generate one with: openssl rand -base64 24
```

**3. Start:**

```bash
docker compose pull && docker compose up -d
```

**4. Open `https://<PUBLIC_HOST>/`** and join a room with its PIN from `config.yaml`. The
certificate is self-signed, so each browser asks to accept it once.

**5. Open the firewall** to your users: TCP 443 (web) and port 7000 over UDP and TCP (audio and
video).

### Everyday tasks

| Task | How |
|---|---|
| Apply changes to `config.yaml` | `docker compose restart server player` |
| Read the logs | `journalctl -t kunveno-server -f` |
| Monitoring | `https://<PUBLIC_HOST>/grafana/`, user `admin`, the password from `.env` |
| Use your own certificate | replace `certs/fullchain.pem` and `certs/privkey.pem`, then `docker compose restart web` |
| Back up | copy the directory (`data/` is written by root, so with `sudo`) |

**Upgrade** to a new release, in the directory:

```bash
docker compose pull && docker compose up -d
```

The stack runs the `latest` images, so this fetches the new release and restarts only the
containers whose image changed; `.env`, `config.yaml` and the data stay. To stay on one release,
set `KUNVENO_VERSION=0.2.4` (for example) in `.env`.

### If something does not work

- **Users join but hear and see nothing:** the server guessed the wrong address for media. Add
  `KUNVENO_ADVERTISE_IP=<the address users reach, e.g. the VPN one>` to `.env` and run
  `docker compose up -d`.
- **Ports 80 or 443 are taken:** set `HTTP_PORT` and `HTTPS_PORT` in `.env`.
- **The server stops with "config.yaml is a directory":** the file went missing and Docker created
  a directory in its place; remove the directory, restore `config.yaml` from the archive and start
  again.

All `.env` settings are described in `.env` itself, all `config.yaml` settings in
[Configuration](#configuration). Certificates, the firewall, troubleshooting and the Debian packages
are in [docs/deploy.md](docs/deploy.md).

## For administrators

**Where the media goes.** kunveno is a selective forwarding unit (SFU): every participant sends
the server one stream per kind (voice, camera, screen) and the server sends copies to everyone
else. Nothing travels between participants directly, so each participant's upstream stays small
and the load lands on the server's network link.

**Encryption.** Media is encrypted with DTLS-SRTP between each browser and the server, so on the
wire everything is encrypted. The server decrypts the streams to forward them; there is no
end-to-end encryption between participants. The site and signaling go over HTTPS.

**Bandwidth.** Per participant, roughly: voice about 40 kbit/s, a camera about 350 kbit/s, screen
sharing up to 2.5 Mbit/s (`media.share_max_kbps`). The server receives one stream of each kind
from every participant and sends (N − 1) copies of each. For 15 people with cameras on and one
screen shared that is about 120 Mbit/s outbound from the server in the worst case; a voice-only
meeting of the same size needs under 10 Mbit/s.

**Ports.** Everything runs on the one host:

| Port | What it carries |
|---|---|
| TCP 443 | the site (HTTPS) and signaling (WebSocket through nginx) |
| TCP 80 | a redirect to 443, nothing else |
| UDP 7000 | the media channel: voice and video of all participants on one port; no per-call ports are opened |
| TCP 7000 | the fallback media channel for a participant whose UDP does not get through (typically a NAT or a firewall on a gateway that drops long UDP flows); the browser switches to it on its own and keeps it for a week, and "try the fast one" next to the status switches back |

No STUN, TURN or other external service is involved. The server's own ports (API, metrics,
Prometheus, Grafana, node-exporter) listen on 127.0.0.1 only; Grafana is reached through 443 at `/grafana/`.
The dashboard's "Заметки" row (collapsed) belongs to the optional kunveno-notes add-on, which adds its
Prometheus target through `/etc/prometheus/targets/*.json`; without it there is no target and the row is empty.
The "Запись" row (collapsed) works the same way for the kunveno-recorder add-on.

**Firewall rule.** Allow from your users' networks to the host: TCP 443 (and 80), UDP 7000 and
TCP 7000. If a network segment can only use TCP 7000, letting its UDP 7000 through gives those
users the faster path.

**Screen sharing** is the browser's own feature (`getDisplayMedia`); nothing is installed on
users' computers. It works in Chrome and Firefox on Linux and Windows.

## Meeting notes

A participant can switch on notes in a room (More → Take notes). The notes bot then joins the room
as a participant named "Notes", listens to each person's microphone separately and sends the
recognised phrases to the server, which keeps the text: who said what, and when. Everyone in the
room sees "Notes" with a sheet icon in the header. When the notes are switched off, or the last
person leaves, everyone still in the room gets the bar "Ready after the meeting:" with a "Notes"
button that downloads them; the lobby offers the same file under "Last meeting", with the room PIN. The file is Markdown: the room,
the date and time, who spoke, and the phrases in order, with pauses marked.

- The bot is the commercial add-on `kunveno-notes` (speech recognition on the server's processor,
  no external service, no audio stored). It is not part of this repository; for a licence write to
  ssubbotin@gmail.com. Its installation guide comes with the licence.
- Without the add-on keep `notes.enabled: false` (the default): rooms and the lobby show nothing
  about notes.
- Only the last meeting of each room is kept, for `notes.keep` (one day by default) after its notes
  end (a switch-off or the end of the meeting); the first phrase of the next meeting replaces it.
  Deleting a room deletes its notes. Downloading needs the room PIN, and wrong PINs count toward
  the same block as joins.
- The notes can be deleted earlier from the lobby: "Last meeting" → "Delete…" with the room PIN,
  then "Notes" (or "Everything") in the confirmation. Notes still being taken cannot be deleted: switch them
  off first.
- `notes.live: true` shows the phrases in the room as they are recognised, in the tile of the
  "Notes" participant, for checking the recognition.

## Meeting recording

A participant can switch on recording in a room (More → Recording). The recorder bot then
joins the room as a participant named "Recording" and records the shared screen and everyone's audio
as one video (1920x1080, 15 frames per second, H.264 and AAC). While nobody shares a screen, the
video shows a slate with the room name, the date and the current speaker. Cameras are not recorded.
Everyone in the room sees "● Recording" in the header; the red dot always means this video. When
recording is switched off, or the last person leaves, the bot uploads the file to the server, and
everyone still in the room gets a "Video" button in the bar "Ready after the meeting:" (next to
"Notes" when the meeting had notes too, with one close button for both); the lobby offers the same
file under "Last meeting", with the room PIN.

The lobby shows "Last meeting" under a room that keeps a video, notes or both, with what is there
and the nearest expiry ("Video and notes, kept until 10/16, 2:43 PM"). Its dialog asks for the room
PIN once and lists each item with its own expiry and a "Download" button.

- The recorder is the commercial add-on `kunveno-recorder`. It is not part of this repository; for a
  licence write to ssubbotin@gmail.com. Its installation guide comes with the licence.
- Without the add-on keep `recording.enabled: false` (the default): rooms and the lobby show nothing
  about recording.
- Only the last meeting of each room is kept, for `recording.keep` (one day by default) after it
  ends; the next recording replaces it. Switching recording on again in the same meeting also replaces what
  was recorded before: a meeting has one recording, and the later one wins. Deleting a room deletes
  its recording. Downloading needs the room PIN, and wrong PINs count toward the same block as
  joins. An interrupted download can be resumed while its link is valid (5 minutes); afterwards start
  the download again from the room or the lobby.
- The recording can be deleted earlier from the lobby: "Last meeting" → "Delete…" with the room
  PIN, then "Video" (or "Everything") in the confirmation. A recording in progress cannot be deleted: switch it off first.
- A file larger than `recording.max_size` (8 GB by default) is refused by the server and stays in the
  add-on's volume, so size the limit for the longest meeting and keep free disk space for it.

## How it works

![How kunveno fits together](docs/images/architecture.svg)

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
  the echo plays a room's sound back with a delay when someone asks for it; the player either plays
  a configured video file in a loop or relays a live RTSP camera, as its own camera tile (a live
  feed is marked as such). It uses ffmpeg: a file is transcoded once to VP8 and Opus and
  cached, a feed is transcoded on the fly and reconnected every 5 s if it drops.

Certificates, the firewall, troubleshooting and the helpers in detail: [docs/deploy.md](docs/deploy.md).

## Repository layout

| Path | What |
|---|---|
| `cmd/kunveno-server` | server process: configuration, HTTP, signaling, media |
| `cmd/kunveno-echo`, `cmd/kunveno-player` | the echo, and the player for video files and RTSP cameras |
| `internal/room`, `internal/signal`, `internal/sfu` | room state, WebSocket signaling, the SFU |
| `internal/config`, `internal/httpapi`, `internal/metrics` | configuration, HTTP API, Prometheus metrics |
| `internal/quality`, `internal/ratelimit`, `internal/roomstore` | connection grades, PIN limits, saved rooms |
| `internal/echo`, `internal/player`, `pkg/botclient` | helper internals |
| `web/` | the browser client |
| `deploy/` | Docker images and compose stack, nginx, systemd unit, deb packaging, example configuration |
| `tools/loadgen` | synthetic participants for load and end-to-end checks |
| `docs/` | operations guide, manual checks before a release, the architecture diagram and screenshots |

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

Running locally: copy `deploy/kunveno/config.yaml`, set `media.advertise_ip` to the machine's LAN
address (browsers gather no loopback candidates, so `127.0.0.1` gives no media), start
`bin/kunveno-server -config <file>`, then `cd web && npm run dev` (Vite proxies `/ws` and `/api/`
to `127.0.0.1:8080`). `go run ./tools/loadgen -n 14 -duration 30s` adds synthetic participants (`-camera <picture or
directory>` gives them cameras showing still pictures, `-names a,b,c` names them; needs ffmpeg). Before a release, walk
through the manual checks in [docs/test-matrix.md](docs/test-matrix.md).

## Configuration

`config.yaml` (read at start; the server refuses unknown keys):

| Key | Meaning |
|---|---|
| `http.listen` | address nginx proxies to, on loopback (default `KUNVENO_UPSTREAM` from `.env`, else `127.0.0.1:8080`) |
| `media.advertise_ip` | the address browsers send media to (default `KUNVENO_ADVERTISE_IP` from `.env`, else the host's primary IPv4 address) |
| `media.udp_port`, `media.tcp_port` | media ports (7000 by default; `tcp_port: -1` turns the TCP fallback off) |
| `media.share_max_kbps` | bitrate cap for screen sharing |
| `ui.title` | title shown in the lobby and the browser tab |
| `ui.reactions` | emoji participants can send as reactions (1..16; ten by default) |
| `ui.noise_suppression` | `true` turns the client's noise filter (GTCRN) on for participants who have not chosen themselves (default `false`) |
| `rooms[]` | `slug`, `name`, `pin`, `max_participants`, and optionally `player: {file, name}` for a video loop from the media directory or `player: {url, name}` for a live camera (`rtsp://` or `rtsps://`, credentials in the URL are masked in logs) |
| `rooms_file` | where rooms created in the lobby are saved (`/var/lib/kunveno/rooms.json`) |
| `backgrounds_dir` | folder with the virtual background images (`/var/lib/kunveno/backgrounds`; the stack mounts `backgrounds/` there) |
| `notes.enabled` | `true` offers meeting notes in rooms; needs the `kunveno-notes` add-on (default `false`, which hides every notes control) |
| `notes.keep` | how long notes stay after they end: a whole number and `m`, `h` or `d` (`90m`, `12h`, `1d`, `2d`; default `1d`) |
| `notes.live` | `true` shows phrases in the room as they are recognised, for checking the recognition (default `false`) |
| `notes.dir` | where the notes are kept (`/var/lib/kunveno/notes`, which is `data/notes` in the stack) |
| `recording.enabled` | `true` offers meeting recording in rooms; needs the `kunveno-recorder` add-on (default `false`, which hides every recording control) |
| `recording.keep` | how long a recording stays after it ends: a whole number and `m`, `h` or `d` (`90m`, `12h`, `1d`, `2d`; default `1d`) |
| `recording.dir` | where recordings are kept (`/var/lib/kunveno/recordings`, which is `data/recordings` in the stack) |
| `recording.max_size` | the largest recording the server accepts: a whole number and `MB` or `GB` (`500MB`, `8GB`; default `8GB`) |

`.env` next to the compose file sets the release (`KUNVENO_VERSION`), ports, the public host name
for Grafana and its admin password.

## Operations

- Logs: `journalctl -t kunveno-server --since "7 days ago" -o cat` (also `kunveno-web`,
  `kunveno-echo`, `kunveno-player`, `kunveno-prometheus`, `kunveno-grafana`, `kunveno-node-exporter`). Each connection logs
  its media path (UDP or TCP), quality changes and wrong PINs.
- Metrics: `/metrics` on the server (loopback only); Grafana at `https://<host>/grafana/` with the
  provisioned "kunveno" dashboard.
- Data to keep on the host, all next to `docker-compose.yml`: `config.yaml`, `.env`, `data/` (rooms
  created in the lobby, the player's cache), `certs/` and `media/`.
- An RTSP camera must be reachable from the host; the player pulls it over TCP. A room whose feed is
  down keeps the player as a participant without video until the feed returns.

## Versions

Releases are git tags `vX.Y.Z` on merge commits to `main`; the version shows in the lobby footer
and in the `kunveno_build_info` metric. A tag publishes the images
`ghcr.io/ssubbotin/kunveno-{server,web,echo,player}` as `X.Y.Z` and `latest` and a GitHub
release with `kunveno.tgz`, the stack directory with `docker-compose.yml` pinned to that
version. `main` publishes `main`, and a pull request from this repository publishes a
preview `pr-<number>` (`KUNVENO_VERSION=pr-<number>` in `.env` to try it).

## License

Copyright (C) 2026 Sergey Subbotin <ssubbotin@gmail.com>

kunveno is free software: you can redistribute it and/or modify it under the terms of the
[GNU Affero General Public License, version 3](LICENSE) (`AGPL-3.0-only`). In short: you may run,
study, change and share it; if you run a modified version as a network service, you must offer its
users the source of that version.

A commercial license is available for organizations that cannot accept the AGPL terms, for
example to ship kunveno inside a proprietary product, to keep their changes private, or to offer
kunveno as a hosted or resold service without publishing their changes. Commercial terms also
cover support. Write to ssubbotin@gmail.com.

The name "kunveno" identifies the original project; forks should use a different name.
