#!/usr/bin/env bash
# Rebuild playmore and restart the systemd service. Run after code changes.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "==> Building"
go build -o playmore

echo "==> Restarting playmore.service"
sudo systemctl restart playmore

echo "==> Status"
systemctl --no-pager --lines=5 status playmore
