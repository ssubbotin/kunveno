#!/bin/sh
# Entrypoint of the server and player images. The compose stack bind-mounts ./data from the host at
# /var/lib/kunveno, so the directory's owner is whoever unpacked the stack. The container starts as
# root only to look at it: the service then runs as that owner (or as 10001 when the directory
# belongs to root, as one that Docker creates does), with no root rights left. Files that releases before
# this one wrote as root are handed to the owner once.
set -eu
dir=/var/lib/kunveno

[ "$(id -u)" = 0 ] || exec "$@"

mkdir -p "$dir"
uid=$(stat -c %u "$dir")
gid=$(stat -c %g "$dir")
if [ "$uid" = 0 ]; then
  uid=10001 gid=10001
  chown "$uid:$gid" "$dir"
fi
# Read-only mounts inside (backgrounds) are left alone.
find "$dir" -path "$dir/backgrounds" -prune -o -user 0 -exec chown -h "$uid:$gid" {} + 2>/dev/null ||
  echo "kunveno: some files in $dir could not be handed to $uid:$gid" >&2

cfg=/etc/kunveno/config.yaml
if [ -e "$cfg" ] && ! su-exec "$uid:$gid" test -r "$cfg"; then
  echo "kunveno: $cfg is not readable by $uid:$gid, the owner of ./data: chmod 644 config.yaml, or give both the same owner" >&2
  exit 1
fi
exec su-exec "$uid:$gid" "$@"
