#!/bin/sh
# check-user.sh IMAGE: the server and player images run as user 10001, never as root, and need no
# capabilities. The bind-mounted ./data must belong to 10001; when it does not, the server stops at
# once with the command that fixes it instead of failing later. Run from the repository root.
set -eu
img=$1
dir=$(mktemp -d)
trap 'docker run --rm -v "$dir:/d" alpine:3.22 rm -rf /d/ok /d/root >/dev/null 2>&1; rm -rf "$dir"' EXIT
fail() { echo "check-user: $*" >&2; exit 1; }

user=$(docker image inspect -f '{{.Config.User}}' "$img")
[ "$user" = 10001 ] || fail "the image runs as \"$user\", want 10001"

case $img in *kunveno-server*) ;; *) echo "check-user: $img runs as 10001"; exit 0 ;; esac

# The server with the stack's config and no capabilities at all, on a ./data in $1; prints its log
# after 3 s (it either stopped by then or is serving) and removes it.
serve() {
  cid=$(docker run -d --cap-drop ALL --security-opt no-new-privileges -e KUNVENO_ADVERTISE_IP=127.0.0.1 \
    -v "$PWD/deploy/kunveno/config.yaml:/etc/kunveno/config.yaml:ro" -v "$1:/var/lib/kunveno" "$img")
  sleep 3
  docker logs "$cid" 2>&1
  docker rm -f "$cid" > /dev/null
}

docker run --rm -v "$dir:/d" alpine:3.22 sh -c 'mkdir -p /d/ok /d/root && chown 10001:10001 /d/ok'

# ./data owned by 10001: the server starts.
out=$(serve "$dir/ok")
echo "$out" | grep -q '"msg":"kunveno-server started"' || fail "data owned by 10001: the server did not start: $out"

# ./data owned by root (as Docker creates a missing directory): it stops and names the fix.
out=$(serve "$dir/root")
echo "$out" | grep -q 'chown -R 10001:10001' || fail "data owned by root: no hint to chown: $out"
echo "check-user: $img runs as 10001 and names the fix for a root-owned ./data"
