#!/bin/sh
# Runs from the nginx image entrypoint (/docker-entrypoint.d) before nginx starts. Sets up the
# optional password wall nginx puts in front of /grafana/, for a server that faces the internet:
# with GRAFANA_WALL_PASSWORD set, nobody without it reaches Grafana's own code (user
# GRAFANA_WALL_USER, default admin; only a hash is stored). Without it /grafana/ stays as it was,
# with Grafana's login only. The location includes the file written here either way.
set -eu

conf=/etc/nginx/kunveno-grafana-wall.conf
file=/etc/nginx/kunveno-grafana.htpasswd
user=${GRAFANA_WALL_USER:-admin}
pass=${GRAFANA_WALL_PASSWORD:-}

if [ -z "$pass" ]; then
  : > "$conf"
  rm -f "$file"
  echo "kunveno: /grafana/ has no password wall (set GRAFANA_WALL_PASSWORD on a server that faces the internet)" >&2
  exit 0
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
# The wall's credentials stay here: Grafana keeps its own login.
cat > "$conf" <<EOF
auth_basic "Monitoring";
auth_basic_user_file $file;
proxy_set_header Authorization "";
EOF
