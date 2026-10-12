#!/bin/sh
set -e
systemctl daemon-reload || true
systemctl enable kunveno-server.service || true
if systemctl is-active --quiet kunveno-server.service; then systemctl restart kunveno-server.service; fi
echo "kunveno-server installed. Edit /etc/kunveno/config.yaml, then: systemctl start kunveno-server"
