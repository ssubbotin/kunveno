#!/bin/sh
# check-image.sh IMAGE: fail if a published image would leak sources or debug data.
#   - no source or map files (.go .ts .svelte .map, go.mod, package.json, tsconfig*.json)
#   - no sourceMappingURL in the web bundle
#   - Go binaries built with -trimpath (no /src/ build paths) and without DWARF sections
set -eu
img=$1
status=0

cid=$(docker create "$img")
trap 'docker rm -f "$cid" >/dev/null' EXIT
list=$(docker export "$cid" | tar -t)

leaks=$(printf '%s\n' "$list" | grep -E '\.(go|ts|svelte|map)$|(^|/)(go\.(mod|sum)|package(-lock)?\.json|tsconfig[^/]*\.json|vite\.config\.[jt]s)$' || true)
if [ -n "$leaks" ]; then
  echo "source or map files in $img:" >&2
  printf '%s\n' "$leaks" | head -50 >&2
  status=1
fi

if printf '%s\n' "$list" | grep -q '^usr/share/nginx/kunveno/'; then
  if docker run --rm --entrypoint sh "$img" -c 'grep -rl sourceMappingURL /usr/share/nginx/kunveno' >&2; then
    echo "sourceMappingURL in the web bundle of $img" >&2
    status=1
  fi
fi

for bin in $(printf '%s\n' "$list" | grep -E '^usr/local/bin/kunveno-[a-z]+$' || true); do
  if docker run --rm --entrypoint sh "$img" -c "grep -aqE '/src/(cmd|internal)/' /$bin"; then
    echo "$bin in $img keeps build paths (built without -trimpath?)" >&2
    status=1
  fi
  if docker run --rm --entrypoint sh "$img" -c "grep -aq '\.debug_info' /$bin"; then
    echo "$bin in $img carries DWARF debug data (built without -s -w?)" >&2
    status=1
  fi
done

[ "$status" -eq 0 ] && echo "$img: no sources, maps or debug data"
exit "$status"
