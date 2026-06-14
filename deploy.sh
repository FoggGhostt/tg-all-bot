#!/usr/bin/env bash
# Build the bot for linux and deploy it to a server running tg-all-bot.service.
#
# Usage:
#   ./deploy.sh <ssh-target> [<bot-token>]
#
#   ./deploy.sh root@161.104.34.193
#                       # deploy binary only (token on server stays as-is)
#   ./deploy.sh root@161.104.34.193 1234567890:AAA...
#                       # deploy binary AND update /etc/tg-all-bot.env with new token
#   ARCH=arm64 ./deploy.sh root@my-arm-vps
#                       # arm64 build
#
# Assumes the first-time setup is already done on the server:
#   /etc/systemd/system/tg-all-bot.service
#   /etc/tg-all-bot.env  (with BOT_TOKEN=...)
#   /var/lib/tg-all-bot/  (owned by tg-all-bot user)

set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <ssh-target> [<bot-token>]" >&2
    echo "       $0 root@host                              # deploy binary only" >&2
    echo "       $0 root@host 1234:AAA...                  # deploy binary + update token" >&2
    echo "       ARCH=arm64 $0 root@host                   # arm64 build" >&2
    exit 1
fi

TARGET="$1"
TOKEN="${2:-}"
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

if [[ -n "${TOKEN}" ]]; then
    echo "==> update /etc/tg-all-bot.env"
    # Token is interpolated locally and piped via stdin — the remote shell
    # never sees the literal token in its command line / process list.
    printf 'BOT_TOKEN=%s\n' "${TOKEN}" | ssh "${TARGET}" \
        'umask 077 && cat > /etc/tg-all-bot.env && chmod 600 /etc/tg-all-bot.env'
fi

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
