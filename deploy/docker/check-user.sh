#!/bin/sh
# check-user.sh IMAGE: the server and player images start as root only to hand over their data
# directory, then run the service as its owner (start-as-owner.sh). Runs the image with the
# capabilities the compose stack gives it and checks who the service runs as:
#   - ./data owned by 1234: the service runs as 1234, and a file an earlier release left as root
#     becomes 1234's;
#   - ./data owned by root (a fresh directory made by Docker): the service runs as 10001, which now
#     owns the directory.
# `id` stands in for the service (the image's CMD is replaced). Run from the repository root.
set -eu
img=$1
caps="--cap-drop ALL --cap-add CHOWN --cap-add SETUID --cap-add SETGID --cap-add DAC_OVERRIDE --security-opt no-new-privileges"
dir=$(mktemp -d)
trap 'docker run --rm -v "$dir:/d" alpine:3.22 rm -rf /d/data /d/root >/dev/null 2>&1; rm -rf "$dir"' EXIT

fail() { echo "check-user: $*" >&2; exit 1; }

# A data directory owned by 1234 with a file a root-run release left behind.
docker run --rm -v "$dir:/d" alpine:3.22 sh -c 'mkdir -p /d/data/notes && touch /d/data/notes/old.jsonl && chown 1234:1234 /d/data'
# shellcheck disable=SC2086
got=$(docker run --rm $caps -v "$dir/data:/var/lib/kunveno" "$img" sh -c 'id -u; stat -c %u /var/lib/kunveno/notes/old.jsonl')
[ "$got" = "1234
1234" ] || fail "data owned by 1234: got $(echo "$got" | tr '\n' ' '), want 1234 1234"

# A data directory owned by root.
docker run --rm -v "$dir:/d" alpine:3.22 mkdir -p /d/root
# shellcheck disable=SC2086
got=$(docker run --rm $caps -v "$dir/root:/var/lib/kunveno" "$img" sh -c 'id -u; stat -c %u /var/lib/kunveno')
[ "$got" = "10001
10001" ] || fail "data owned by root: got $(echo "$got" | tr '\n' ' '), want 10001 10001"

# Nothing left with root's rights.
# shellcheck disable=SC2086
caps_now=$(docker run --rm $caps -v "$dir/data:/var/lib/kunveno" "$img" sh -c 'grep CapEff /proc/self/status')
case $caps_now in *0000000000000000) ;; *) fail "the service keeps capabilities: $caps_now" ;; esac
echo "check-user: $img runs as the owner of its data"
