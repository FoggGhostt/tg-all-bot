#!/usr/bin/env bash
# Build the bot for linux and deploy it to a server running tg-all-bot.service.
#
# Usage:
#   ./deploy.sh root@161.104.34.193
#   ARCH=arm64 ./deploy.sh root@my-arm-vps
#
# Assumes the first-time setup is already done on the server:
#   /etc/systemd/system/tg-all-bot.service
#   /etc/tg-all-bot.env  (with BOT_TOKEN=...)
#   /var/lib/tg-all-bot/  (owned by tg-all-bot user)

set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <ssh-target>" >&2
    echo "       e.g. $0 root@161.104.34.193" >&2
    echo "       arch via env: ARCH=arm64 $0 root@host  (default: amd64)" >&2
    exit 1
fi

TARGET="$1"
ARCH="${ARCH:-amd64}"
REMOTE_BIN="/usr/local/bin/tg-all-bot"
LOCAL_BIN="dist/tg-all-bot-linux-${ARCH}"

if [[ ! -f go.mod ]]; then
    echo "error: run this from the project root (go.mod not found)" >&2
    exit 1
fi

echo "==> build linux/${ARCH}"
mkdir -p dist
GOOS=linux GOARCH="${ARCH}" CGO_ENABLED=0 go build \
    -trimpath -ldflags="-s -w" \
    -o "${LOCAL_BIN}" .
ls -lh "${LOCAL_BIN}"

echo "==> upload to ${TARGET}"
scp "${LOCAL_BIN}" "${TARGET}:${REMOTE_BIN}.new"

echo "==> swap + restart"
ssh "${TARGET}" "
    set -e
    mv ${REMOTE_BIN}.new ${REMOTE_BIN}
    chmod +x ${REMOTE_BIN}
    systemctl restart tg-all-bot
    sleep 1
    systemctl is-active tg-all-bot
    journalctl -u tg-all-bot -n 10 --no-pager
"

echo "==> done"
