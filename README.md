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
- Docker + Docker Compose
- Bot token от [@BotFather](https://t.me/BotFather)
- В BotFather: **Bot Settings → Group Privacy → Turn off** (иначе бот будет видеть только команды, не все сообщения). Альтернатива — сделать бота админом группы.

### Одна команда
```bash
git clone git@github.com:FoggGhostt/tg-all-bot.git
cd tg-all-bot
echo 'BOT_TOKEN=<твой_токен>' > .env
docker compose up -d --build
```

Логи:
```bash
docker compose logs -f
```

База лежит в Docker volume `bot-data` — переживает пересборку и рестарт.

### Если Docker Hub не открывается (например, сервер в РФ)

Тебе нужно настроить registry mirror на хосте, иначе `docker compose build` упрётся в timeout на `registry-1.docker.io`:

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

Проверь:
```bash
docker info | grep -A4 "Registry Mirrors"
```

После этого `docker compose up -d --build` пройдёт.

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

## Локальный запуск без Docker

```bash
BOT_TOKEN=<токен> DB_PATH=./bot.db go run .
```
