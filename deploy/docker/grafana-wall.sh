#!/bin/sh
# Runs from the nginx image entrypoint (/docker-entrypoint.d) before nginx starts. Writes the
# password file of the wall nginx puts in front of /grafana/, so that nobody without it reaches
# Grafana's own code. The user is GRAFANA_WALL_USER (default admin); the password is
# GRAFANA_WALL_PASSWORD, or GRAFANA_ADMIN_PASSWORD when that is not set. Only a hash is stored.
# Without any password nginx does not start: the wall never opens by itself.
set -eu

file=/etc/nginx/kunveno-grafana.htpasswd
user=${GRAFANA_WALL_USER:-admin}
pass=${GRAFANA_WALL_PASSWORD:-${GRAFANA_ADMIN_PASSWORD:-}}

if [ -z "$pass" ]; then
  echo "kunveno: set GRAFANA_WALL_PASSWORD or GRAFANA_ADMIN_PASSWORD in .env (the password of /grafana/)" >&2
  exit 1
fi
case $user in
  *:*|'') echo "kunveno: GRAFANA_WALL_USER must be non-empty and contain no colon" >&2; exit 1 ;;
esac

hash=$(printf '%s' "$pass" | openssl passwd -6 -stdin)
umask 027
printf '%s:%s\n' "$user" "$hash" > "$file.tmp"
# Workers read the file on every request; they run as the nginx user.
chown root:nginx "$file.tmp" 2>/dev/null || true
mv "$file.tmp" "$file"
