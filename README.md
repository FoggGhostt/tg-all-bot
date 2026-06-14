# tg-all-bot

Telegram-бот, который умеет в один тег позвать всех известных ему участников чата — даже тех, кто замьютил группу. Написан на Go, хранит участников в SQLite, разворачивается одной командой через Docker Compose.

## Команды

| Команда | Что делает |
|---|---|
| `/all` (а также `/все`, `/всех`, `/здесь`, `/everyone`, `/here`) | Позвать всех известных мне участников через скрытые эмодзи-ссылки. Без тегов бота, без стены `@username`. |
| `/add @user1 @user2 …` | **Только админ.** Добавить участников в базу вручную. Полезно сразу после добавления бота, пока он ещё никого не запомнил по сообщениям. |
| `/count` | Сколько участников я уже знаю в этом чате. |
| `/help` | Список команд. |

### Пример `/all`
```
Иван Поляков запустил призыв.

🐧 🦊 🐢 🐳 🦄 🐙 🦋 🐝 🦉 🐬
🌈 🌟 ⚡ 🍕 🦒

Призыв окончен.
```
Каждый эмодзи — это скрытая mention-ссылка на конкретного юзера. Notification приходит даже если чат замьючен (это поведение mention-сущностей в Telegram, не магия бота).

## Как пополняется база участников

Бот учится **по сообщениям**: запомнил, кто что писал — может тегать. Это ограничение Bot API: Telegram **не даёт ботам читать историю чата** или получать полный список участников группы. Поэтому:

1. **Авто (live-learning).** Каждый, кто пишет в чат, попадает в базу. Кто-то ушёл из чата — удаляется. Это идёт само, ничего делать не надо.
2. **Руками через `/add`.** Если кто-то ещё не писал — добавь его сам:
   ```
   /add @vasya @petya @kolya
   ```
   Бот резолвит каждый `@username` через `getChat` и сохраняет user_id. Работает для юзеров с **публичным** username.

   Для юзеров **без публичного username** (или скрывших его): начни писать `@` в чате, выбери человека из автокомплита Telegram — он подставится как кликабельное имя (это `text_mention`-сущность). Бот возьмёт user_id прямо из неё, username знать не обязательно.

   ⚠️ Если бот никогда раньше не пересекался с этим юзером и у того скрыт username — Telegram просто не отдаст его ID. Тогда один из вариантов: попросить юзера написать одно сообщение в чат, после этого `/all` будет его пинговать всегда.

## Деплой

### Требования
- Bot token от [@BotFather](https://t.me/BotFather)
- В BotFather: **Bot Settings → Group Privacy → Turn off** (иначе бот будет видеть только команды, не все сообщения). Альтернатива — сделать бота админом группы.

### Способ 1 — голый бинарь + systemd (рекомендую)

Самый лёгкий путь для слабого сервера в РФ. Собираешь бинарь у себя локально, заливаешь на сервер, systemd держит его живым. **На сервере не нужны ни Docker, ни Go, ни сеть до GitHub/Docker Hub.**

**Локально** (Mac/Linux):
```bash
git clone git@github.com:FoggGhostt/tg-all-bot.git && cd tg-all-bot
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
    -trimpath -ldflags="-s -w" -o dist/tg-all-bot-linux-amd64 .

scp dist/tg-all-bot-linux-amd64 root@server:/usr/local/bin/tg-all-bot
scp deploy/tg-all-bot.service root@server:/etc/systemd/system/
```

**На сервере** (один раз):
```bash
useradd --system --no-create-home --shell /usr/sbin/nologin tg-all-bot
mkdir -p /var/lib/tg-all-bot && chown tg-all-bot:tg-all-bot /var/lib/tg-all-bot
chmod +x /usr/local/bin/tg-all-bot

cat > /etc/tg-all-bot.env <<'EOF'
BOT_TOKEN=<твой_токен>
EOF
chmod 600 /etc/tg-all-bot.env

systemctl daemon-reload
systemctl enable --now tg-all-bot
systemctl status tg-all-bot
```

Логи:
```bash
journalctl -u tg-all-bot -f
```

Обновление потом — одна команда из репо:
```bash
./deploy.sh root@161.104.34.193
```
Скрипт собирает свежий бинарь, заливает его на сервер под временным именем, делает атомарный `mv` (чтобы не упереться в `ETXTBSY` от запущенного процесса), рестартит сервис и показывает первые строки логов. Если ARM-сервер: `ARCH=arm64 ./deploy.sh root@host`.

Руками то же самое:
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
    -trimpath -ldflags="-s -w" -o dist/tg-all-bot-linux-amd64 .
scp dist/tg-all-bot-linux-amd64 root@server:/usr/local/bin/tg-all-bot.new
ssh root@server 'mv /usr/local/bin/tg-all-bot.new /usr/local/bin/tg-all-bot && chmod +x /usr/local/bin/tg-all-bot && systemctl restart tg-all-bot'
```

База лежит в `/var/lib/tg-all-bot/bot.db` — переживает рестарты и реинсталлы бинаря.

> Если сервер не amd64, замени `GOARCH=amd64` на `arm64`/`arm` соответственно. Узнать на сервере: `uname -m`.

### Способ 2 — Docker через ghcr.io

Образ собирается в GitHub Actions и пушится в `ghcr.io/foggghostt/tg-all-bot:latest`. **На сервере сборки нет** — только `docker compose pull`. Требует, чтобы сервер мог достучаться до ghcr.io и `pkg-containers.githubusercontent.com` (иногда последний приджевывается в РФ).

После первого пуша в `main` сделай пакет публичным:
1. Открой <https://github.com/FoggGhostt/tg-all-bot/pkgs/container/tg-all-bot>
2. **Package settings** → **Change visibility** → **Public**

На сервере:
```bash
mkdir -p ~/tg-all-bot && cd ~/tg-all-bot
curl -O https://raw.githubusercontent.com/FoggGhostt/tg-all-bot/main/docker-compose.yml
echo 'BOT_TOKEN=<твой_токен>' > .env
docker compose pull
docker compose up -d
```

Обновление:
```bash
docker compose pull && docker compose up -d
```

### Способ 3 — Docker, но билд локально

Если ghcr.io не открывается и Docker Hub тоже:
```bash
# локально
docker build -t tg-all-bot:latest .
docker save tg-all-bot:latest | gzip | ssh root@server 'gunzip | docker load'

# на сервере — docker-compose.yml уже там, .env создан как выше
docker compose up -d
```

### Локальная разработка

Без Docker:
```bash
BOT_TOKEN=<токен> DB_PATH=./bot.db go run .
```
С Docker:
```bash
docker compose up -d --build
```

## Конфиг

| ENV | Дефолт | Что |
|---|---|---|
| `BOT_TOKEN` | — (обязательно) | Токен от BotFather |
| `DB_PATH` | `/data/bot.db` | Путь к SQLite-файлу. Volume в docker-compose уже подмонтирован. |

## Как это устроено внутри

- **SQLite** (`modernc.org/sqlite`, pure-Go — без CGO, поэтому Docker-сборка простая). Таблица `chat_users(chat_id, user_id, username, first_name, last_name, last_seen)`.
- На каждое сообщение в группе — `UPSERT` отправителя.
- На `new_chat_members` — добавить, на `left_chat_member` — удалить.
- На `/all` — список из БД → батчи по 50 эмодзи-ссылок → отправка с задержкой 1.2 с между батчами (чтобы не упереться в лимит Telegram «20 msg/min в одну группу»).
- На `/add` — `text_mention`-entities парсятся напрямую (там есть `User` объект с ID), а `@username`-mentions резолвятся через `getChat`.
- **CI/CD**: `.github/workflows/docker.yml` собирает мульти-слойный образ и пушит в `ghcr.io` на каждый push в `main`. Сборка кешируется через `type=gha` — повторные билды быстрые.
