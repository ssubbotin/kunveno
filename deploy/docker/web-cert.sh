#!/bin/sh
# Runs from the nginx image entrypoint (/docker-entrypoint.d) before nginx starts. When the
# certificate directory has no certificate yet, issues a self-signed one for PUBLIC_HOST and
# TLS_NAMES (more host names and IP addresses, comma-separated) so that a fresh install serves
# HTTPS at once. An existing certificate is never touched; to use your own, replace fullchain.pem
# and privkey.pem in ./certs and restart the container.
set -eu

dir=/etc/ssl/kunveno
[ -s "$dir/fullchain.pem" ] && [ -s "$dir/privkey.pem" ] && exit 0

names=${PUBLIC_HOST:-localhost}${TLS_NAMES:+,$TLS_NAMES}
san=
cn=
for n in $(echo "$names" | tr ',' ' '); do
  [ -n "$cn" ] || cn=$n
  if echo "$n" | grep -Eq '^[0-9.]+$|:'; then san="$san,IP:$n"; else san="$san,DNS:$n"; fi
done
san=${san#,}

if ! [ -w "$dir" ]; then
  echo "kunveno: no certificate in $dir and it is not writable" >&2
  exit 1
fi
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
  -subj "/CN=$cn" -addext "subjectAltName=$san" \
  -keyout "$dir/privkey.pem" -out "$dir/fullchain.pem" 2>/dev/null
chmod 600 "$dir/privkey.pem"
# The files belong to the owner of the directory, so replacing them needs no root.
chown "$(stat -c %u:%g "$dir")" "$dir/privkey.pem" "$dir/fullchain.pem"
echo "kunveno: issued a self-signed certificate for $san (valid 825 days)"
