package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "modernc.org/sqlite"
)

const (
	mentionsPerMessage = 50
	mentionsPerLine    = 10
	sendDelay          = 1200 * time.Millisecond
)

const welcomeText = "Привет! Я запоминаю участников этого чата по их сообщениям. " +
	"Когда нужно позвать всех — напиши /all (также /все, /everyone, /здесь).\n\n" +
	"Дополнительно: /add @user1 @user2 — добавить участников вручную (если кто-то ещё не писал в чат).\n\n" +
	"Чтобы я видел сообщения всех, отключи приватность в @BotFather " +
	"(Bot Settings → Group Privacy → Turn off) или сделай меня админом."

var emojiPool = []string{
	"🐧", "🦊", "🐢", "🐳", "🦄", "🐙", "🦋", "🐝", "🦉", "🐬",
	"🦦", "🦔", "🐢", "🐲", "🦒", "🐨", "🐼", "🐸", "🐰", "🦝",
	"🌈", "🌟", "⚡", "🔥", "💫", "☀️", "🌙", "⭐", "🍀", "🌸",
	"🍕", "🍔", "🌮", "🍣", "🍦", "🍩", "☕", "🍷", "🍓", "🍑",
	"🚀", "🎮", "🎨", "🎭", "🎲", "🎸", "🎺", "🎯", "🏆", "🎁",
	"📚", "✏️", "🔮", "💎", "🎈", "🎉", "🌺", "🦜", "🪐", "🌊",
}

var usernameRe = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_]{4,31})`)

type User struct {
	ID        int64
	Username  string
	FirstName string
	LastName  string
}

func displayName(u User) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = "участник"
	}
	return name
}

func callerName(u *tgbotapi.User) string {
	if u == nil {
		return "Кто-то"
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name != "" {
		return name
	}
	if u.UserName != "" {
		return u.UserName
	}
	return "Кто-то"
}

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS chat_users (
			chat_id    INTEGER NOT NULL,
			user_id    INTEGER NOT NULL,
			username   TEXT    NOT NULL DEFAULT '',
			first_name TEXT    NOT NULL DEFAULT '',
			last_name  TEXT    NOT NULL DEFAULT '',
			last_seen  INTEGER NOT NULL,
			PRIMARY KEY (chat_id, user_id)
		);
	`); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Upsert(chatID int64, u User) error {
	_, err := s.db.Exec(`
		INSERT INTO chat_users (chat_id, user_id, username, first_name, last_name, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, user_id) DO UPDATE SET
			username   = excluded.username,
			first_name = excluded.first_name,
			last_name  = excluded.last_name,
			last_seen  = excluded.last_seen
	`, chatID, u.ID, u.Username, u.FirstName, u.LastName, time.Now().Unix())
	return err
}

func (s *Store) Remove(chatID, userID int64) error {
	_, err := s.db.Exec(`DELETE FROM chat_users WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	return err
}

func (s *Store) List(chatID int64) ([]User, error) {
	rows, err := s.db.Query(`
		SELECT user_id, username, first_name, last_name
		FROM chat_users WHERE chat_id = ?
		ORDER BY last_seen DESC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.FirstName, &u.LastName); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) Close() error { return s.db.Close() }

type Bot struct {
	api    *tgbotapi.BotAPI
	store  *Store
	selfID int64
}

func (b *Bot) handle(update tgbotapi.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	switch msg.Chat.Type {
	case "private":
		b.handlePrivate(msg)
		return
	case "group", "supergroup":
	default:
		return
	}

	if msg.From != nil && !msg.From.IsBot {
		if err := b.store.Upsert(msg.Chat.ID, User{
			ID:        msg.From.ID,
			Username:  msg.From.UserName,
			FirstName: msg.From.FirstName,
			LastName:  msg.From.LastName,
		}); err != nil {
			log.Printf("upsert sender: %v", err)
		}
	}

	for _, m := range msg.NewChatMembers {
		if m.ID == b.selfID {
			b.send(tgbotapi.NewMessage(msg.Chat.ID, welcomeText))
			continue
		}
		if m.IsBot {
			continue
		}
		if err := b.store.Upsert(msg.Chat.ID, User{
			ID:        m.ID,
			Username:  m.UserName,
			FirstName: m.FirstName,
			LastName:  m.LastName,
		}); err != nil {
			log.Printf("upsert new member: %v", err)
		}
	}

	if msg.LeftChatMember != nil && msg.LeftChatMember.ID != b.selfID {
		if err := b.store.Remove(msg.Chat.ID, msg.LeftChatMember.ID); err != nil {
			log.Printf("remove left member: %v", err)
		}
	}

	if !msg.IsCommand() {
		return
	}

	switch strings.ToLower(msg.Command()) {
	case "all", "everyone", "here", "все", "всех", "здесь":
		b.handleTagAll(msg)
	case "add":
		b.handleAdd(msg)
	case "count":
		b.handleCount(msg)
	case "help", "start":
		b.handleHelp(msg)
	}
}

func (b *Bot) handlePrivate(msg *tgbotapi.Message) {
	if !msg.IsCommand() {
		return
	}
	text := "Я бот для группового чата.\n\n" +
		"Добавь меня в группу и используй /all чтобы тегнуть всех известных мне участников.\n\n" +
		"В группе отправь /help для списка команд."
	b.send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func (b *Bot) handleHelp(msg *tgbotapi.Message) {
	text := "Команды:\n" +
		"• /all (также /все, /everyone, /здесь) — тегнуть всех известных участников\n" +
		"• /add @user1 @user2 — добавить участников в базу вручную (только админ)\n" +
		"• /count — сколько участников я уже видел\n" +
		"• /help — эта справка\n\n" +
		"Я запоминаю людей по их сообщениям. Чтобы видеть всех, отключи приватность в @BotFather или сделай меня админом."
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ReplyToMessageID = msg.MessageID
	b.send(reply)
}

func (b *Bot) handleCount(msg *tgbotapi.Message) {
	users, err := b.store.List(msg.Chat.ID)
	if err != nil {
		log.Printf("list: %v", err)
		return
	}
	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Знаю %d участников этого чата.", len(users)))
	reply.ReplyToMessageID = msg.MessageID
	b.send(reply)
}

func (b *Bot) handleTagAll(msg *tgbotapi.Message) {
	users, err := b.store.List(msg.Chat.ID)
	if err != nil {
		log.Printf("list: %v", err)
		return
	}
	if len(users) == 0 {
		reply := tgbotapi.NewMessage(msg.Chat.ID,
			"Я ещё никого не запомнил в этом чате. Подожди, пока люди напишут хотя бы по одному сообщению, или добавь руками через /add @user1 @user2.")
		reply.ReplyToMessageID = msg.MessageID
		b.send(reply)
		return
	}

	caller := callerName(msg.From)
	total := len(users)

	for i := 0; i < total; i += mentionsPerMessage {
		end := i + mentionsPerMessage
		if end > total {
			end = total
		}
		chunk := users[i:end]

		var body strings.Builder
		if i == 0 {
			body.WriteString(escapeHTML(caller))
			body.WriteString(" запустил призыв.\n\n")
		}
		body.WriteString(buildEmojiMentions(chunk))
		if end == total {
			body.WriteString("\n\nПризыв окончен.")
		}

		out := tgbotapi.NewMessage(msg.Chat.ID, body.String())
		out.ParseMode = "HTML"
		out.DisableWebPagePreview = true
		b.send(out)
		if end < total {
			time.Sleep(sendDelay)
		}
	}
}

func (b *Bot) handleAdd(msg *tgbotapi.Message) {
	if msg.From == nil || !b.isAdmin(msg.Chat.ID, msg.From.ID) {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Только админ чата может добавлять участников вручную.")
		reply.ReplyToMessageID = msg.MessageID
		b.send(reply)
		return
	}

	seen := map[int64]bool{}
	var added, failed []string

	for _, e := range msg.Entities {
		if e.Type == "text_mention" && e.User != nil && !e.User.IsBot {
			if seen[e.User.ID] {
				continue
			}
			seen[e.User.ID] = true
			u := User{
				ID:        e.User.ID,
				Username:  e.User.UserName,
				FirstName: e.User.FirstName,
				LastName:  e.User.LastName,
			}
			if err := b.store.Upsert(msg.Chat.ID, u); err != nil {
				log.Printf("add upsert: %v", err)
				failed = append(failed, displayName(u))
				continue
			}
			added = append(added, displayName(u))
		}
	}

	seenName := map[string]bool{}
	for _, m := range usernameRe.FindAllStringSubmatch(msg.Text, -1) {
		uname := m[1]
		key := strings.ToLower(uname)
		if seenName[key] {
			continue
		}
		seenName[key] = true

		chat, err := b.api.GetChat(tgbotapi.ChatInfoConfig{
			ChatConfig: tgbotapi.ChatConfig{SuperGroupUsername: "@" + uname},
		})
		if err != nil {
			failed = append(failed, "@"+uname)
			continue
		}
		if chat.Type != "private" {
			failed = append(failed, "@"+uname+" (не пользователь)")
			continue
		}
		if seen[chat.ID] {
			continue
		}
		seen[chat.ID] = true

		u := User{
			ID:        chat.ID,
			Username:  chat.UserName,
			FirstName: chat.FirstName,
			LastName:  chat.LastName,
		}
		if err := b.store.Upsert(msg.Chat.ID, u); err != nil {
			log.Printf("add upsert: %v", err)
			failed = append(failed, "@"+uname)
			continue
		}
		added = append(added, displayName(u))
	}

	var sb strings.Builder
	if len(added) > 0 {
		fmt.Fprintf(&sb, "Добавлено: %d\n%s", len(added), strings.Join(added, ", "))
	}
	if len(failed) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "Не получилось: %s\n\n", strings.Join(failed, ", "))
		sb.WriteString("Если юзер скрыл @username — выбери его через автокомплит при вводе @, тогда я смогу взять его user_id напрямую. Если бот никогда с ним не пересекался, Telegram не отдаст ID.")
	}
	if sb.Len() == 0 {
		sb.WriteString("Не нашёл упоминаний. Используй: /add @user1 @user2 …  (или начни вводить @ и выбери из автокомплита для юзеров без публичного username)")
	}

	reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	reply.ReplyToMessageID = msg.MessageID
	b.send(reply)
}

func (b *Bot) isAdmin(chatID, userID int64) bool {
	member, err := b.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: chatID,
			UserID: userID,
		},
	})
	if err != nil {
		log.Printf("getChatMember: %v", err)
		return false
	}
	return member.Status == "administrator" || member.Status == "creator"
}

func (b *Bot) send(c tgbotapi.Chattable) {
	if _, err := b.api.Send(c); err != nil {
		log.Printf("send: %v", err)
	}
}

func buildEmojiMentions(users []User) string {
	var b strings.Builder
	for i, u := range users {
		if i > 0 {
			if i%mentionsPerLine == 0 {
				b.WriteString("\n")
			} else {
				b.WriteString(" ")
			}
		}
		fmt.Fprintf(&b, `<a href="tg://user?id=%d">%s</a>`, u.ID, pickEmoji())
	}
	return b.String()
}

func pickEmoji() string {
	return emojiPool[rand.IntN(len(emojiPool))]
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func run(ctx context.Context) error {
	token := strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if token == "" {
		return errors.New("BOT_TOKEN env var is required")
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/bot.db"
	}

	store, err := NewStore(dbPath)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer store.Close()

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	log.Printf("authorized as @%s (id=%d)", api.Self.UserName, api.Self.ID)

	b := &Bot{api: api, store: store, selfID: api.Self.ID}

	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = 30
	cfg.AllowedUpdates = []string{"message"}
	updates := api.GetUpdatesChan(cfg)

	go func() {
		<-ctx.Done()
		log.Println("shutting down")
		api.StopReceivingUpdates()
	}()

	for update := range updates {
		b.handle(update)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}
