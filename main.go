package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "modernc.org/sqlite"
)

const (
	mentionsPerMessage = 5
	sendDelay          = 1200 * time.Millisecond
)

const welcomeText = "Привет! Я запоминаю участников этого чата по их сообщениям. " +
	"Когда нужно позвать всех — напиши /all (также /все, /everyone, /здесь).\n\n" +
	"Чтобы я видел сообщения всех, отключи приватность в @BotFather " +
	"(Bot Settings → Group Privacy → Turn off) или сделай меня админом."

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
			"Я ещё никого не запомнил в этом чате. Подожди, пока люди напишут хотя бы по одному сообщению.")
		reply.ReplyToMessageID = msg.MessageID
		b.send(reply)
		return
	}

	for i := 0; i < len(users); i += mentionsPerMessage {
		end := i + mentionsPerMessage
		if end > len(users) {
			end = len(users)
		}
		out := tgbotapi.NewMessage(msg.Chat.ID, buildMentions(users[i:end]))
		out.ParseMode = "HTML"
		out.DisableWebPagePreview = true
		if i == 0 {
			out.ReplyToMessageID = msg.MessageID
		}
		b.send(out)
		if end < len(users) {
			time.Sleep(sendDelay)
		}
	}
}

func (b *Bot) send(c tgbotapi.Chattable) {
	if _, err := b.api.Send(c); err != nil {
		log.Printf("send: %v", err)
	}
}

func buildMentions(users []User) string {
	parts := make([]string, 0, len(users))
	for _, u := range users {
		parts = append(parts, fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, u.ID, html.EscapeString(displayName(u))))
	}
	return strings.Join(parts, " ")
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
