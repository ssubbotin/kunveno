# Deploying kunveno on Ubuntu (24.04 or 26.04)

## 1. Host
- An Ubuntu host on the VPN. Note its VPN address (example `10.8.0.1`): it becomes `media.advertise_ip`.
- Packages: `sudo apt install nginx`.
- Firewall: allow TCP 443 and UDP 7000 from the VPN subnet only, for example
  `sudo ufw allow from 10.8.0.0/24 to any port 7000 proto udp` and
  `sudo ufw allow from 10.8.0.0/24 to any port 443 proto tcp`.

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
- Stuck at "connecting": the browser cannot reach `advertise_ip:7000/udp`. Check the firewall and
  that `advertise_ip` is the address the browser can route to.
- `bad_message` on join: the request did not reach `/ws` as an upgrade; check the nginx
  `Upgrade`/`Connection` headers.
- 403 on `/ws`: Origin mismatch. Set `http.allowed_origins` in the config to the site URL.

## Docker Compose (alternative to the deb packages)
nginx and the server run as two containers with host networking. Only built artifacts go to the host.

On the workstation: `make docker-bundle`, then `rsync -a dist/kunveno-docker/ <host>:kunveno/`.

On the host, in `~/kunveno`:
```
cp config.example.yaml config.yaml   # advertise_ip, rooms, PINs; pick a free http.listen port
cp env.example .env                  # KUNVENO_UPSTREAM must match http.listen
mkdir -p certs                       # fullchain.pem and privkey.pem (see section 2)
mkdir -p data && sudo chown 10001 data && chmod 700 data   # rooms created in the lobby
docker compose up -d --build
```
Upgrade: rsync a new bundle (it never overwrites `config.yaml`, `.env` or `certs/`), then
`docker compose up -d --build`. Logs: `docker compose logs -f server`.
