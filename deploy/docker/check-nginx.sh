#!/bin/sh
# check-nginx.sh: run `nginx -t` on both site configs inside the nginx image the web image is built on.
#   - deploy/docker/nginx.conf.template, rendered with envsubst the way the image entrypoint does
#   - deploy/nginx/kunveno.conf (the deb install), mounted as conf.d/default.conf
# A self-signed certificate stands in for the real one. Run from the repository root.
set -eu
img=nginx:1.30-alpine-slim
docker run --rm -v "$PWD/deploy:/deploy:ro" "$img" sh -c '
  set -eu
  apk add --no-cache openssl >/dev/null
  mkdir -p /etc/ssl/kunveno /var/www/kunveno
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=check \
    -keyout /etc/ssl/kunveno/privkey.pem -out /etc/ssl/kunveno/fullchain.pem 2>/dev/null
  rm -f /etc/nginx/conf.d/default.conf
  echo "== deploy/nginx/kunveno.conf"
  cp /deploy/nginx/kunveno.conf /etc/nginx/conf.d/default.conf
  nginx -t
  echo "== deploy/docker/nginx.conf.template"
  export HTTP_PORT=80 HTTPS_PORT=443 SERVER_NAME=meet.example.internal KUNVENO_UPSTREAM=127.0.0.1:8080 GRAFANA_PORT=3000
  envsubst "\$HTTP_PORT \$HTTPS_PORT \$SERVER_NAME \$KUNVENO_UPSTREAM \$GRAFANA_PORT" \
    < /deploy/docker/nginx.conf.template > /etc/nginx/conf.d/default.conf
  nginx -t
'
