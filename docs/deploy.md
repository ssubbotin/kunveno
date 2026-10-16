# Running kunveno

The README covers setup: unpack `kunveno.tgz`, fill in two values in `.env`, then `make up`. This guide holds what comes after: certificates,
the firewall, troubleshooting, the helpers, monitoring, previews of pull requests, and the Debian
packages as an alternative to Docker (appendix).

## The Docker Compose stack

nginx with the client, the server and the echo and player helpers run from public images that CI
builds and publishes to GHCR (`.github/workflows/images.yml`): `ghcr.io/ssubbotin/kunveno-server`,
`-web`, `-echo`, `-player`; Prometheus, Grafana and node-exporter are the upstream images. A tag `vX.Y.Z`
publishes `X.Y.Z` and `latest` and a GitHub release with `kunveno.tgz`: the stack directory
`deploy/kunveno` (`docker-compose.yml`, which runs `latest` unless `.env` sets `KUNVENO_VERSION`, a `Makefile` with
`make up`, `make down`, `make pull` and `make import-data`, `.env`, `config.yaml`, `grafana/` with
the data source and the dashboard, empty `certs/` and `media/`). Pushes to `main`
publish `main` and `sha-<commit>`; a pull request from a branch of this repository publishes a
preview `pr-<number>`. CI checks the kunveno images before pushing (`deploy/docker/check-image.sh`):
no sources, no source maps, Go binaries built with `-trimpath -s -w`.

What the stack does on its own:

- the server, the player and echo run as user 10001, never as root (`USER` in their images), and
  keep what they write (rooms created in the lobby, meeting summaries, notes, recordings, the
  player's cache) in the named volume `kunveno_data`, which Docker creates owned by 10001 from the
  images' `/var/lib/kunveno`. `make down` keeps it; `docker compose down -v` deletes it. On the host
  `config.yaml`, `media/` and `backgrounds/` only need to be readable by others, as the archive ships
  them; an unreadable one is named at start with its fix. Releases before 0.4.5 kept the data in
  `./data`: `make import-data` copies it into the volume once;
- every service runs without capabilities (nginx keeps the few it lists in `docker-compose.yml`) and
  with `no-new-privileges`; nginx's master runs as root for ports 80/443 and the files in `certs/`,
  its workers as the nginx user;
- `http.listen` and `media.advertise_ip` come from `.env` (`KUNVENO_UPSTREAM`,
  `KUNVENO_ADVERTISE_IP`) when `config.yaml` leaves them out, and the address from the host's
  default route when neither sets it; the `started` log line shows the result;
- nginx sends security headers (`deploy/docker/security-headers*.conf`, the same files in the Debian
  package): on every response `nosniff`, `no-referrer` and a Permissions-Policy for camera,
  microphone and screen capture; on the client's pages also a Content-Security-Policy that allows
  only its own scripts, WebAssembly, its worker and requests to this host, and forbids framing by
  other sites. Grafana under `/grafana/` keeps its own page policy. No nginx version.
  Strict-Transport-Security is sent only with `KUNVENO_HSTS` in `.env` (for example
  `max-age=31536000`), on a server with a public certificate and HTTPS on port 443: browsers that
  have seen it go to port 443 for this host whatever `HTTPS_PORT` says;
- the web container issues a self-signed certificate for `PUBLIC_HOST` and `TLS_NAMES` into
  `certs/` when it is empty, owned by the directory's owner.

**Trying a pull request on the host.** Add `KUNVENO_VERSION=pr-<number>` to `.env`, run `make up`,
and remove the line afterwards (then `make up` again) to return to the latest release.

**When a release changes the stack files.** `make up` updates the images only. When a release
changes `docker-compose.yml`, the `Makefile` or `grafana/` (its notes say so), take them from the
archive first, in the stack directory; `.env`, `config.yaml` and the data stay:

```bash
curl -fsSL https://github.com/ssubbotin/kunveno/releases/latest/download/kunveno.tgz | tar xz --strip-components=1 kunveno/docker-compose.yml kunveno/Makefile kunveno/grafana kunveno/backgrounds
make up
```

This also restores the built-in background images if you deleted them.

**Logs** go to the host's systemd journal (Docker `journald` driver), so they survive container
recreation and reboots; retention follows the journal. Last week of server logs:
`journalctl -t kunveno-server --since "7 days ago" -o cat`; follow live with `-f`. The other
services log as `kunveno-web`, `kunveno-echo`, `kunveno-player`, `kunveno-prometheus`,
`kunveno-grafana`, `kunveno-node-exporter`.

## TLS certificate

The stack starts with a self-signed certificate, which every browser asks to accept once. To
replace it, put `fullchain.pem` and `privkey.pem` into `certs/` and run
`docker compose restart web`. Where to get one:

- **Your company's CA**, when there is one: the browsers already trust it, so nothing is asked.
- **Self-signed with your own names**, for example a host name and the VPN address:
  `openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 -subj "/CN=meet.example.internal" -addext "subjectAltName=DNS:meet.example.internal,IP:10.8.0.1" -keyout certs/privkey.pem -out certs/fullchain.pem`
  (the stack issues the same kind of certificate on first start, for `PUBLIC_HOST` and
  `TLS_NAMES` from `.env`).
- **Let's Encrypt via the DNS-01 challenge**, when the host name is in a public DNS zone even
  though the host itself is private. The HTTP-01 challenge needs the host reachable from the
  internet on port 80, which a VPN host usually is not.

## Firewall

Allow from your users' networks to the host: TCP 443 (and 80, which only redirects), UDP 7000 and
TCP 7000. With `ufw`, for a VPN subnet `10.8.0.0/24`:

```
sudo ufw allow from 10.8.0.0/24 to any port 443 proto tcp
sudo ufw allow from 10.8.0.0/24 to any port 7000 proto udp
sudo ufw allow from 10.8.0.0/24 to any port 7000 proto tcp
```

UDP 7000 carries the media of all participants on one port; TCP 7000 is the fallback for a
participant whose UDP does not get through (typically a NAT or a firewall on a gateway that drops
long UDP flows). A participant on the fallback still works; letting their UDP through gives them the
faster path. Nothing else is reachable from outside: the API, metrics, Prometheus and Grafana
listen on 127.0.0.1, and Grafana is served through 443 at `/grafana/`.

## Troubleshooting

- Each session logs its route as `media path` (transport udp or tcp, remote address). A
  participant whose connection drops to `disconnected` after a few seconds over UDP has a broken
  UDP path (NAT or firewall on a gateway); the browser falls back to TCP 7000 when UDP fails
  outright.
- A page that drops its own session reports why in its next join. The server logs it at INFO as
  `rejoin` (room, participant, name, reason, after in seconds, transport) and counts it in
  `kunveno_client_restarts_total{reason}`. Reasons: `ws_closed` (signaling socket closed),
  `media_disconnected` (the media connection stayed down past `media.reconnect_grace`, 15 s by
  default), `media_failed`, `connect_timeout` (no media connection within 8 s), `media_error` (the
  server reported `media_failed`), `rtc_error` (the browser could not create the peer connection).
  Frequent `media_disconnected` from one participant points at a lossy path (VPN); raise
  `media.reconnect_grace` if their outages last longer than the default.
- Users join but hear and see nothing: the server advertises an address they cannot reach. Set
  `KUNVENO_ADVERTISE_IP` in `.env` to the address users actually use (the VPN one, for example)
  and run `docker compose up -d`; the `started` log line shows `advertise_ip`.
- Stuck at "connecting": the browser cannot reach `advertise_ip:7000` over UDP or TCP. Check the
  firewall.
- `bad_message` on join: the request did not reach `/ws` as a WebSocket upgrade; check the
  `Upgrade` and `Connection` headers if nginx is not the one from the stack.
- 403 on `/ws`: Origin mismatch. Set `http.allowed_origins` in `config.yaml` to the site URL when
  users open the site under a name other than the one nginx sees.
- The server stops with "config.yaml is a directory": the file went missing and Docker created a
  directory in its place; remove the directory, restore `config.yaml` from the archive and start
  again.

## Sound check (echo)

The `echo` service runs next to the server and stays idle. When someone presses the echo button in
a room, it joins that room as a participant and plays the room's audio back after `ECHO_DELAY`
(2 s by default), one speaker at a time. Pressing the button again, or the last person leaving,
removes it. It reaches the server on loopback (`/internal/echo`, never proxied by nginx).

## Looped video rooms (player)

A room with `player: {file: <name>}` in `config.yaml` gets a participant that plays the file in a
loop, video on its tile and sound as audio, always on. Put the file in `media/` next to
`docker-compose.yml` (any format ffmpeg reads). On start the `player` service transcodes it once to
VP8 640x360 at 15 fps with a keyframe every 2 s, and Opus, and caches the result in
the data volume (`player-cache`); changing the file re-encodes it. The player joins over loopback only, and only
rooms with `player:` accept it. It takes one place in the room and sends about 0.5 Mbit/s to the
server while running. `journalctl -t kunveno-player` shows the transcoding and the loop rounds.

## Live camera rooms (RTSP)

`player: {url: rtsp://user:password@camera:554/stream}` instead of `file:` plays a camera's feed:
the player probes it for sound, encodes it live with ffmpeg (VP8 640x360 at 15 fps, keyframe every
2 s, Opus) and sends frames as they arrive, one ffmpeg process per camera. The feed is read over
TCP. When the camera is unreachable the player stays in the room and retries every 5 s. The
password is masked in the logs; `config.yaml` keeps it, so keep the file readable by you alone. The
player container uses the host network, so it reaches cameras the host can reach.

## Meeting notes

Notes need the commercial `kunveno-notes` add-on, which ships its own image and a compose overlay
with an installation guide. With the add-on installed, set `notes.enabled: true` in `config.yaml`
and run `docker compose restart server`; with `false` (the default) the server ignores the notes
switch, refuses the notes bot, lists no rooms at `/internal/notes`, leaves `notes_until` out of
`/api/rooms`, answers downloads with 404, and the client shows nothing about notes.

- **Storage.** The server writes `notes/<room>.jsonl` in the data volume (mode 0600), one line per phrase plus
  the meeting's header, pauses and end; a restart loses nothing already written, and a file cut off
  by a restart ends at its last line. Markdown is built on every download, in the server's time
  zone (the stack mounts the host's `/etc/localtime`). Notes expire `notes.keep` after the notes
  end (a switch-off or the end of the meeting) and are swept every minute, also while
  `notes.enabled` is off; deleting a room deletes them, also while notes are disabled.
- **Access.** Downloading needs the room PIN (`POST /api/rooms/<room>/notes` with
  `{"pin":"…","lang":"ru|en|es"}`), and wrong PINs count toward the same block as joins.
  Deleting them early needs the PIN too (`DELETE /api/rooms/<room>/notes` with `{"pin":"…"}`):
  204 when deleted; 403 for a wrong PIN, which counts toward the same block as joins; 429 with
  `Retry-After` while the address is blocked; 409 while the room's notes are still being taken or
  finished (the bot has not left yet); 404 when there are none. `/api/rooms` shows only until when
  a room's notes are kept.
- **Debugging the recognition.** `notes.live: true` sends each phrase to the room as it arrives; the
  client shows the phrases in the tile of the "Notes" participant (during a screen share, the
  latest phrase under its name).

**The notes bot contract.** The add-on depends on this interface of the server; it changes only
together with the add-on (the same text is the package comment of `internal/signal/protocol.go`):

1. `GET /internal/notes` on loopback (never proxied by nginx) returns a JSON list of the slugs of
   rooms that want notes; the bot polls it every 2 s. The list is empty while `notes.enabled` is off.
2. The bot joins each listed room over loopback with `{"type":"join","room":…,"name":…,"role":"notes"}`
   (no PIN, through `pkg/botclient`); the join is refused unless the room wants notes. It sends no
   media and receives every person's audio track (stream id = participant id); no video, no other
   helper's audio.
3. Each recognised phrase goes to the server as
   `{"type":"phrase","speaker":<participant id>,"start":<unix ms>,"end":<unix ms>,"text":…}`. A
   malformed frame (wrong field types, a missing speaker, start or end) gets `bad_message` and the
   server closes the socket. A well-formed phrase that fails the content checks is ignored and the
   socket stays open: text 1..2000 characters after every whitespace run is collapsed to one space
   and the ends are trimmed, start positive and not after end, end at most 60 s ahead of the
   server's clock, a known speaker. The server drops a phrase of an unknown speaker; people who
   left during the meeting stay known.
4. When the room no longer wants notes (switched off, or the last person left) the server sends
   `{"type":"stop"}`; the bot finishes its pending phrases, sends them, then `{"type":"leave"}`. A bot
   still there 10 s after the stop is closed. The notes are ready only after the bot has left (or the grace closed it).
5. A bot that loses its socket while the room still wants notes rejoins and sends what it recognised
   meanwhile. A bot is bound to the meeting it joined and cannot write into a later one.

The Debian packages have no notes add-on; with them leave `notes.enabled` off.

## Meeting recording

Recording needs the commercial `kunveno-recorder` add-on, which ships its own image and a compose
overlay with an installation guide. With the add-on installed, set `recording.enabled: true` in
`config.yaml` and run `docker compose restart server`; with `false` (the default) the server
ignores the recording switch, refuses the recorder, lists no rooms at `/internal/recording`, leaves
`recording_until` out of `/api/rooms`, answers downloads with 404, and the client shows nothing
about recording. The recorder needs CPU for the video encoder and disk for the file (an hour of
meeting is a few hundred megabytes); `recording.max_size` (default 8GB) caps one file.

- **Storage.** The server writes `recordings/<room>.mp4` in the data volume and `<room>.json` (the meeting's
  start and end), both mode 0600, in a directory of mode 0700. An upload in progress is a `*.tmp`
  file; the server removes leftover ones at start, so a restart during an upload leaves no broken
  recording. Recordings expire `recording.keep` after the meeting ends and are swept every minute,
  also while `recording.enabled` is off; deleting a room deletes its recording.
- **Access.** Downloading needs the room PIN: `POST /api/rooms/<room>/recording` with
  `{"pin":"…"}` returns a link, valid for 5 minutes, of the form
  `/api/rooms/<room>/recording/<token>`. The GET on that link streams the file and supports
  `Range`, so an interrupted download resumes while the link is valid; after the 5 minutes the download
  starts again from the room or the lobby. nginx streams the response without temporary files
  (both the Docker and the Debian nginx configs have a dedicated location for it). The token is part
  of the URL and so appears in nginx access logs; it is a 5-minute link. Wrong PINs count toward
  the same block as joins. Deleting early needs the PIN too (`DELETE /api/rooms/<room>/recording`
  with `{"pin":"…"}`): 204 when deleted; 403 for a wrong PIN; 429 with `Retry-After` while the
  address is blocked; 409 while the room is being recorded; 404 when there is none. `/api/rooms`
  shows only until when a room's recording is kept and the summary of its meeting (see below).

**The recorder bot contract.** The add-on depends on this interface of the server; it changes only
together with the add-on (the same text is the package comment of `internal/signal/protocol.go`):

1. `GET /internal/recording` on loopback (never proxied by nginx) returns a JSON list of the slugs
   of rooms that want a recording; the bot polls it every 2 s. The list is empty while
   `recording.enabled` is off.
2. The bot joins with `{"type":"join","room":…,"name":…,"role":"recorder"}` (no PIN). The join is
   refused unless the room wants a recording, and while another recorder of the room that was not
   told to stop is there. The `joined` message carries `meeting`: the start of the meeting
   (RFC 3339), which the upload names. The bot sends no media and receives every person's audio
   (stream id = participant id), the current sharer's screen and every person's camera (stream id
   = participant id + `/camera`); nothing from other helpers. A camera switched off stops sending;
   the `participants` message says whose camera is on.
3. When the room no longer wants a recording the server sends `{"type":"stop"}`. The bot finishes
   the file, uploads it with `PUT /internal/recording/<slug>` (loopback only, body `video/mp4`,
   header `X-Kunveno-Meeting`: the meeting start from `joined`) and sends `{"type":"leave"}`. A bot
   still there 60 s after the stop is closed; its upload is still accepted.
4. Upload answers: 204 stored (the room hears `recording_ready`), 400 no meeting header, 404
   recording off or no such room, 409 a later meeting's recording is stored, 413 larger than
   `recording.max_size`. An upload for the same meeting replaces the stored one, so a recorder that
   recorded nothing (no audio and no screen received, for example stopped right after joining) must
   not upload.

The Debian packages have no recorder add-on; with them leave `recording.enabled` off.

## Sign-in with Yandex ID

Off by default. Turn it on for a server that faces the internet; the user-facing description is in
the README ("Sign-in and guests"). Needed: a public `https://` address with a certificate browsers
accept (see "TLS certificate"), and an administrator of the Yandex 360 organization to register
the application.

1. **Register the OAuth application** at <https://oauth.yandex.ru/client/new>. Platform: "Web
   services". Redirect URI: `<public_url>/auth/yandex/callback`, for example
   `https://meet.example.com/auth/yandex/callback`. Permissions: only "Access to username, first
   name and surname, gender" (`login:info`). Save the ID and the password of the application.
2. **The secret** goes into `.env` next to `docker-compose.yml`, never into `config.yaml`:
   ```
   KUNVENO_AUTH_YANDEX_SECRET=<the application's password>
   ```
3. **`auth:` in `config.yaml`:**
   ```yaml
   auth:
     enabled: true
     public_url: https://meet.example.com
     session: 12h
     invite_max: 168h
     providers:
       - type: yandex
         name: "Yandex 360"
         client_id: "<the application's ID>"
         allow: ["*@example.com"]
   ```
   - `public_url` must be `https://<host>` (the host in lower case, `:443` dropped; no path). The
     callback address and the check of the `Origin` header of requests that change something come
     from it, never from the `Host` header. People must open the service at exactly this address.
   - `allow` patterns are matched against the account's Yandex **login**. The login of an
     organization account in Yandex 360 is `name@example.com`; a personal account has a login
     without "@", whatever contact address it lists, so `*@example.com` admits the organization
     and refuses personal accounts. The contact address is ignored on purpose: a personal account
     can attach and confirm an external address there.
   - `session` is how long a sign-in lasts (default `12h`); `invite_max` caps the guest
     invitation (default `168h`; the dialog offers 1 hour, 24 hours and 7 days, never above it).
4. **Apply:** `docker compose up -d` (it passes the new variable to the server container; a plain
   `restart` does not). The server refuses to start with sign-in on and no provider, no
   `public_url` or one that is not `https://`, a provider without `client_id`, without the secret
   in the environment or with an empty `allow`; the message is in `journalctl -t kunveno-server`.
5. **Check:** open the address in a private window: the sign-in screen appears. An organization
   account gets into the lobby; a personal account sees a page saying it is not allowed.

**State.** `auth.key` (the signing key of sessions, 32 random bytes, mode 0600) and `invites.json`
(guest invitations; only the SHA-256 of each token) live next to `rooms_file`: in the stack that
is the `data` volume (`/var/lib/kunveno` inside the server container). Sessions are signed
cookies, not stored on the server, so one session cannot be revoked. To **sign everyone out**
delete the key and restart the server; a new key is generated at the start:

```
docker compose exec server rm /var/lib/kunveno/auth.key && docker compose restart server
```

Employees in meetings stay connected and sign in again for the next one. Invitations stay valid
(they do not depend on the key). Expired and revoked invitations are removed by a periodic sweep.

**Behind the scenes.** nginx proxies `/auth/` to the server like `/api/`. Every `/api/` route
except `/api/info` and `/api/backgrounds` needs a session; `/healthz`, `/metrics` and `/internal/`
are outside the sign-in (the helpers join over loopback as before). The log records sign-ins and
refusals (provider, login) and invitations created, revoked and used; tokens, secrets, cookies and
codes are never logged. A provider that does not answer within 10 seconds counts as unavailable,
and the sign-in screen says so.

**Guests** need no setup: an employee creates a link in a room (More, "Invite a guest"). Revoking
a link disconnects its guests at once; an expired link admits no new guests and those inside stay.

**Going back.** Set `enabled: false` (or remove the `auth:` block) and run `docker compose
restart server`: everything works as before, and `auth.key` and `invites.json` are ignored.

## Meeting summaries

When the last person leaves a room, the server saves the meeting's summary: its start, its end and
the number of people who took part (a person who rejoined under the same name counts once; bots
never count; no names are kept). The summaries live in `meetings.json` next to `rooms_file`
(`meetings.json` in the data volume of the Docker stack, `/var/lib/kunveno/meetings.json` with the Debian
packages), mode 0600, replaced atomically on every change. The file keeps the last 5 meetings of each
room, drops a meeting 30 days after its end (swept every minute), and loses a room's entries when
the room is deleted. A file that cannot be read is logged and replaced: the server starts with no
summaries.

`/api/rooms` lists `last_meeting` (`{"start","end","people"}`, RFC 3339) for a room only while it
keeps notes or a recording of a meeting with a saved summary (the recording's meeting first), and
the lobby shows it as "10/15, 11:00 AM–11:27 AM · 27 min · 7 participants" above the "Last meeting"
caption and in its dialog.

## Monitoring (Prometheus and Grafana)

`kunveno-server` exposes Prometheus metrics at `/metrics` to loopback clients only (nginx does not
proxy it). The stack runs Prometheus on `127.0.0.1:${PROMETHEUS_PORT}` (default 9095, 30 days of
data) and Grafana behind nginx at `https://<PUBLIC_HOST>/grafana/` with the Prometheus data source
and the "kunveno" dashboard provisioned from `grafana/` in the stack directory. Log in as `admin`
with `GRAFANA_ADMIN_PASSWORD` from `.env`; Grafana reads it on its first start only.

On a server that faces the internet, set `GRAFANA_WALL_PASSWORD` (and optionally
`GRAFANA_WALL_USER`, default `admin`) in `.env`: nginx then asks for that password before any
request reaches Grafana, so Grafana's own code is not open to everyone; Grafana's login follows.
Without it nginx logs at start that `/grafana/` has no wall.

The stack also runs node-exporter on `127.0.0.1:${NODE_EXPORTER_PORT}` (default 9105) for the
"Хост" row of the dashboard: CPU load, CPU, disk and memory pressure (Linux PSI), traffic of the
physical interfaces, and packets the host dropped (interface drops, UDP buffer overflows). When
participants lose packets at the same moment, these panels tell load from other software on the
host apart from losses in the network: no drops on the host and no CPU pressure point at the
network between the participants and the host.

The "Заметки" row (collapsed by default) shows the optional kunveno-notes add-on. Prometheus reads
extra scrape targets from `/etc/prometheus/targets/*.json` (job `addons`, file-based discovery);
add-ons drop their target file there, and the kunveno-notes file points at `127.0.0.1:9101` (the
bot's `/metrics`) with `job: kunveno-notes`. The stack ships no target file, so without an add-on
nothing is scraped and nothing shows as down. The row covers queue delay, recognition speed,
queue length, rooms and tracks, phrases, errors, rejoins, the loaded model and the add-on version.
Without the add-on the row has no data.

The "Запись" row (collapsed) shows the optional kunveno-recorder add-on the same way: its target
file points at `127.0.0.1:9102` (the recorder's `/metrics`) with `job: kunveno-recorder`, and the
add-on's compose overlay registers it like the notes one. The row covers rooms being recorded, the
encoder in use, frames per second by source (screen or slate), dropped frames, the pacing lag,
uploads by result and the add-on's errors by stage. The metrics are `kunveno_recorder_rooms`,
`kunveno_recorder_encoder_info{encoder}`, `kunveno_recorder_frames_total{source}`,
`kunveno_recorder_frames_dropped_total`, `kunveno_recorder_audio_tracks`,
`kunveno_recorder_pacing_lag_seconds`, `kunveno_recorder_uploads_total{result}`,
`kunveno_recorder_upload_bytes_total`, `kunveno_recorder_raw_bytes_total` and
`kunveno_recorder_errors_total{stage}`. Without the add-on the row has no data.

## Virtual backgrounds

Participants can replace their camera background with an image. The images are the files in
`backgrounds/` next to `docker-compose.yml` (mounted read-only at `/var/lib/kunveno/backgrounds`;
`backgrounds_dir` in `config.yaml` changes the path). Four built-in images ship in the archive.
Add your own by copying files there: JPEG, PNG or WebP, up to 5 MB and 3840x2160 pixels each, at
most 50 files; the file name without its extension is the label. The server lists the folder on
every request and builds small previews on demand, so no restart is needed. A file that is too
large, too big in pixels or not an image is skipped and logged (`journalctl -t kunveno-server`).
The built-in images are drawn by `go run ./tools/backgrounds`.

## Appendix: Debian packages with the host's own nginx

An alternative to Docker for an Ubuntu host (24.04 or 26.04) that already runs nginx. The packages
are built on a workstation with `make deb` (needs [nfpm](https://nfpm.goreleaser.com)):
`dist/kunveno-server_<version>_amd64.deb` (the server binary, a systemd unit, `config.yaml` under
`/etc/kunveno`) and `dist/kunveno-web_<version>_all.deb` (the client under `/var/www/kunveno` and an
nginx site). CI does not build or test them; the Docker stack is the maintained path.

1. **Certificate:** `fullchain.pem` and `privkey.pem` in `/etc/ssl/kunveno/` (mode 0600 on the
   key), from any of the sources above.
2. **Install:**
   ```
   sudo dpkg -i kunveno-server_<version>_amd64.deb kunveno-web_<version>_all.deb
   sudo editor /etc/kunveno/config.yaml        # advertise_ip, rooms, PINs
   sudo sed -i 's/meet.example.internal/<your host name>/' /etc/nginx/sites-available/kunveno.conf
   sudo ln -sf /etc/nginx/sites-available/kunveno.conf /etc/nginx/sites-enabled/kunveno.conf
   sudo nginx -t && sudo systemctl reload nginx
   sudo systemctl start kunveno-server
   ```
3. **Verify:** `curl -k https://<host>/healthz` prints `ok`, `curl -k https://<host>/api/rooms`
   lists the rooms, `journalctl -u kunveno-server -f` shows a `started` line.
4. **Upgrade:** `make deb` again, copy the two packages, `sudo dpkg -i …`. The configuration file
   is preserved (`noreplace`); the service restarts on its own.

The echo, the player, Prometheus and Grafana have no packages; without them the rooms work, and
the echo button, player rooms and the dashboard are simply absent.

The server package does not create `/var/lib/kunveno` or ship the background images (systemd's
state directory handling owns that path). To offer virtual backgrounds, create
`/var/lib/kunveno/backgrounds` and copy the images from the archive's `backgrounds/` there, or
point `backgrounds_dir` at another directory; without it the list is empty.

Sign-in (`auth:` in `config.yaml`, see "Sign-in with Yandex ID") works with the packages too, with
two differences. The unit has no `EnvironmentFile`, so give the service the secret with a drop-in:
`sudo systemctl edit kunveno-server` and write

```
[Service]
Environment=KUNVENO_AUTH_YANDEX_SECRET=<the application's password>
```

then `sudo systemctl restart kunveno-server` (`systemctl edit` stores the file world-readable under
`/etc/systemd/system/kunveno-server.service.d/`; run `sudo chmod 600` on it to keep the secret from
other users). And `auth.key` and
`invites.json` are in `/var/lib/kunveno`: to sign everyone out run
`sudo rm /var/lib/kunveno/auth.key && sudo systemctl restart kunveno-server`. The nginx site in
the package proxies `/auth/` already.
