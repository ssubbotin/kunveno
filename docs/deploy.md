# Deploying kunveno on Ubuntu (24.04 or 26.04)

## 1. Host
- An Ubuntu host on the VPN. Note its VPN address (example `10.8.0.1`): it becomes `media.advertise_ip`.
- Packages: `sudo apt install nginx`.
- Firewall: allow TCP 443, UDP 7000 and TCP 7000 (ICE-TCP fallback) from the VPN subnets only, for example
  `sudo ufw allow from 10.8.0.0/24 to any port 7000 proto udp` and
  `sudo ufw allow from 10.8.0.0/24 to any port 443 proto tcp` and
  `sudo ufw allow from 10.8.0.0/24 to any port 7000 proto tcp`.

## 2. TLS certificate
Choose one:
- Company CA: place `fullchain.pem` and `privkey.pem` in `/etc/ssl/kunveno/` (mode 0600 on the key).
- Self-signed for the first rollout (browsers need the certificate imported once):
  `sudo mkdir -p /etc/ssl/kunveno && sudo openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 -subj "/CN=meet.example.internal" -addext "subjectAltName=DNS:meet.example.internal,IP:10.8.0.1" -keyout /etc/ssl/kunveno/privkey.pem -out /etc/ssl/kunveno/fullchain.pem`
- Let's Encrypt via DNS-01 if the name is in a public zone.

## 3. Install
```
sudo dpkg -i kunveno-server_<version>_amd64.deb kunveno-web_<version>_all.deb
sudo editor /etc/kunveno/config.yaml        # advertise_ip, rooms, PINs
sudo sed -i 's/meet.example.internal/<your host name>/' /etc/nginx/sites-available/kunveno.conf
sudo ln -sf /etc/nginx/sites-available/kunveno.conf /etc/nginx/sites-enabled/kunveno.conf
sudo nginx -t && sudo systemctl reload nginx
sudo systemctl start kunveno-server
```

## 4. Verify
- `curl -k https://<host>/healthz` prints `ok`.
- `curl -k https://<host>/api/rooms` lists the rooms.
- `journalctl -u kunveno-server -f` shows a `started` line with `udp_port` 7000.
- Two browsers on the VPN can join a room and hear each other.

## 5. Upgrade
`make deb` on the workstation, copy the two debs, `sudo dpkg -i …`. The config file is preserved
(`noreplace`); the service restarts automatically.

## 6. Troubleshooting
- Each session's route is logged as `media path` (transport udp or tcp, remote address); a
  participant whose connection drops to `disconnected` after a few seconds over UDP has a broken
  UDP path (NAT or firewall on a gateway). Browsers fall back to TCP 7000 when UDP fails outright.
- Stuck at "connecting": the browser cannot reach `advertise_ip:7000/udp`. Check the firewall and
  that `advertise_ip` is the address the browser can route to.
- `bad_message` on join: the request did not reach `/ws` as an upgrade; check the nginx
  `Upgrade`/`Connection` headers.
- 403 on `/ws`: Origin mismatch. Set `http.allowed_origins` in the config to the site URL.

## Docker Compose (alternative to the deb packages)
nginx with the client, the server and the echo helper run from images that CI builds and publishes
to GHCR (`.github/workflows/images.yml`): `ghcr.io/ssubbotin/kunveno-server`, `-web`, `-echo`.
A tag `vX.Y.Z` publishes `X.Y.Z` and `latest`; pushes to `main` publish `main` and `sha-<commit>`;
a pull request from a branch of this repository publishes a preview `pr-<number>` (set
`KUNVENO_VERSION=pr-<number>` to try it on the host, then switch back to a release).
CI checks every image before pushing (`deploy/docker/check-image.sh`): no sources, no source maps,
Go binaries built with `-trimpath -s -w`. The images are private.

Once per host, log in to GHCR with a GitHub token that can only read packages (`read:packages`):
`docker login ghcr.io -u <github user>` and paste the token as the password.

On the workstation: `make deploy-bundle`, then `rsync -a dist/kunveno-docker/ <host>:kunveno/`.
It carries the compose file and configuration templates only.

On the host, in `~/kunveno`:
```
cp config.example.yaml config.yaml   # advertise_ip, rooms, PINs; pick a free http.listen port
cp env.example .env                  # KUNVENO_VERSION, KUNVENO_UPSTREAM (must match http.listen)
mkdir -p certs                       # fullchain.pem and privkey.pem (see section 2)
mkdir -p data && sudo chown 10001 data && sudo chmod 700 data   # rooms created in the lobby
docker compose pull && docker compose up -d
```
Upgrade: set `KUNVENO_VERSION` in `.env` to the new release, then `docker compose pull && docker compose up -d`.
Logs go to the host's systemd journal (Docker `journald` driver), so they survive container
recreation and reboots; retention follows the journal. Last week of server logs:
`journalctl -t kunveno-server --since "7 days ago" -o cat`; follow live with `-f`. The other
services log as `kunveno-web`, `kunveno-echo`, `kunveno-prometheus`, `kunveno-grafana`.

### Sound check (echo)
The `echo` service runs next to the server and stays idle. When someone presses "Эхо-тест" in a
room, it joins that room as a participant and plays the room's audio back after `ECHO_DELAY`
(2 s by default), one speaker at a time. Pressing the button again, or the last person leaving,
removes it. It reaches the server on loopback (`/internal/echo`, never proxied by nginx).

### Looped video rooms (player)
A room with `player: {file: <name>}` in `config.yaml` gets a participant that plays the file in a
loop, video on its tile and sound as audio, always on. Put the file in `~/kunveno/media` (any format
ffmpeg reads). On start the `player` service transcodes it once to VP8 640x360 at 15 fps with a
keyframe every 2 s, and Opus, and caches the result in `data/player-cache`; changing the file
re-encodes it. The player joins over loopback only, and only rooms with `player:` accept it. It
takes one place in the room and sends about 0.5 Mbit/s to the server while running.
`journalctl -t kunveno-player` shows the transcoding and the loop rounds.

### Live camera rooms (RTSP)
`player: {url: rtsp://user:password@camera:554/stream}` instead of `file:` plays a camera's feed:
the player probes it for sound, encodes it live with ffmpeg (VP8 640x360 at 15 fps, keyframe every
2 s, Opus) and sends frames as they arrive, one ffmpeg process per camera. The feed is read over
TCP. When the camera is unreachable the player stays in the room and retries every 5 s. The
password is masked in the logs; `config.yaml` keeps it (mode 0640). The player container uses the
host network, so it reaches cameras the host can reach.

### Monitoring (Prometheus and Grafana)
`kunveno-server` exposes Prometheus metrics at `/metrics` to loopback clients only (nginx does not
proxy it). The stack runs Prometheus on `127.0.0.1:${PROMETHEUS_PORT}` (default 9095, 30 days of
data) and Grafana behind nginx at `https://<PUBLIC_HOST>/grafana/` with the "kunveno" dashboard
provisioned. Set `PUBLIC_HOST` and `GRAFANA_ADMIN_PASSWORD` in `.env`; log in as `admin`.
