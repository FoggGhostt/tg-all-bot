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

Образ собирается в GitHub Actions и пушится в GitHub Container Registry (`ghcr.io/foggghostt/tg-all-bot:latest`). На сервере сборки **нет вообще** — только `docker compose pull`. Так что подходит даже слабым VPS, и обходит блокировку Docker Hub.

### Требования
- Docker + Docker Compose
- Bot token от [@BotFather](https://t.me/BotFather)
- В BotFather: **Bot Settings → Group Privacy → Turn off** (иначе бот будет видеть только команды, не все сообщения). Альтернатива — сделать бота админом группы.

### Разовая настройка (один раз после первого push)

После первого пуша в `main` GitHub Actions соберёт образ и положит его в ghcr.io. **По умолчанию пакет приватный** — сделай его публичным, чтобы сервер мог тянуть без логина:

1. Открой <https://github.com/FoggGhostt/tg-all-bot/pkgs/container/tg-all-bot>
2. **Package settings** → **Change visibility** → **Public** → подтверди

(Альтернатива — оставить приватным и логиниться на сервере через `docker login ghcr.io -u <user> -p <PAT>`.)

### Деплой на сервер

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

Логи:
```bash
docker compose logs -f
```

База лежит в Docker volume `bot-data` — переживает пересборку и рестарт.

### Локальная разработка

В `docker-compose.yml` оставлен `build: .`, поэтому для теста изменений локально:
```bash
docker compose up -d --build
```
Это соберёт образ из исходников вместо тянуть из ghcr.

Или без Docker вообще:
```bash
BOT_TOKEN=<токен> DB_PATH=./bot.db go run .
```

### Если ghcr.io тоже недоступен

Тогда либо настрой registry mirror на хосте, либо вернись к локальной сборке. Mirror для Docker Hub (на случай если включишь обратно `build:`):

```bash
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": [
    "https://mirror.gcr.io",
    "https://huecker.io",
    "https://dockerhub.timeweb.cloud"
  ]
}
EOF
systemctl restart docker
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
