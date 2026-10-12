#!/bin/sh
set -e
if ! getent group kunveno >/dev/null; then groupadd --system kunveno; fi
if ! getent passwd kunveno >/dev/null; then
  useradd --system --gid kunveno --home-dir /nonexistent --shell /usr/sbin/nologin kunveno
fi
