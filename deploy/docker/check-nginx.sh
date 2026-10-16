#!/bin/sh
# check-nginx.sh: run `nginx -t` on both site configs inside the nginx image the web image is built on.
#   - deploy/docker/nginx.conf.template, rendered with envsubst the way the image entrypoint does
#   - deploy/nginx/kunveno.conf (the deb install), mounted as conf.d/default.conf
# then starts nginx with the template and checks /grafana/ (a stand-in on port 3000 echoes the
# Authorization header it gets): without GRAFANA_WALL_PASSWORD it is open as before; with it, the
# password wall (grafana-wall.sh) comes first and its credentials never reach Grafana.
# A self-signed certificate stands in for the real one. Run from the repository root.
set -eu
img=nginx:1.30-alpine-slim
docker run --rm -v "$PWD/deploy:/deploy:ro" "$img" sh -c '
  set -eu
  apk add --no-cache openssl curl >/dev/null
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
  GRAFANA_ADMIN_PASSWORD=adminpass sh /deploy/docker/grafana-wall.sh
  nginx -t
  echo "== no wall password: /grafana/ works as before"
  printf "server { listen 127.0.0.1:3000; location / { return 200 \"auth=[\$http_authorization]\\n\"; } }\n" \
    > /etc/nginx/conf.d/fake-grafana.conf
  nginx
  g=https://127.0.0.1/grafana/
  [ "$(curl -sk "$g")" = "auth=[]" ] || { echo "without a wall password /grafana/ is not open" >&2; exit 1; }
  [ "$(curl -sk -u a:b "$g")" = "auth=[Basic YTpi]" ] || { echo "without a wall the Authorization header no longer reaches Grafana" >&2; exit 1; }
  echo "== the wall with a password"
  GRAFANA_WALL_PASSWORD=wallpass sh /deploy/docker/grafana-wall.sh
  nginx -s reload; sleep 1
  code=$(curl -sk -o /dev/null -w "%{http_code}" "$g")
  [ "$code" = 401 ] || { echo "no credentials: $code, want 401" >&2; exit 1; }
  code=$(curl -sk -o /dev/null -w "%{http_code}" -u admin:wrong "$g")
  [ "$code" = 401 ] || { echo "wrong password: $code, want 401" >&2; exit 1; }
  body=$(curl -sk -u admin:wallpass "$g")
  [ "$body" = "auth=[]" ] || { echo "with credentials: got \"$body\", want auth=[] (passed, header removed)" >&2; exit 1; }
  echo "== the wall with its own user"
  GRAFANA_WALL_USER=ops GRAFANA_WALL_PASSWORD=wallpass sh /deploy/docker/grafana-wall.sh
  nginx -s reload; sleep 1
  [ "$(curl -sk -o /dev/null -w "%{http_code}" -u admin:wallpass "$g")" = 401 ] || { echo "the default user still opens the wall" >&2; exit 1; }
  [ "$(curl -sk -u ops:wallpass "$g")" = "auth=[]" ] || { echo "the wall user does not open it" >&2; exit 1; }
  echo ok
'
