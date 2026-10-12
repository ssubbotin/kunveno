# kunveno

Internal voice meetings with desktop sharing for the company VPN. Esperanto: "meeting".

- `cmd/kunveno-server`: Go signaling + SFU server (pion/webrtc).
- `web/`: Svelte 5 web client, built as a separate static service behind nginx.
- `deploy/`: systemd unit, nginx site, deb packaging.


Builds and tests run locally: `make test`, `make build`, `make deb` (see Makefile).
Private repository, internal use.
