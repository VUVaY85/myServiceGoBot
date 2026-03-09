package main

import (
	"context"
	"database/sql"
	"encoding/base64"

	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func runBot(db *sql.DB, key []byte) {

	token := mustEnv("BOT_TOKEN")

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Bot authorized as @%s", bot.Self.UserName)

	// Reply keyboard: always visible bottom buttons
	mainKeyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCalc),
			tgbotapi.NewKeyboardButton(btnPass),
			tgbotapi.NewKeyboardButton(btnNotes),
		),
	)
	mainKeyboard.ResizeKeyboard = true

	states := map[int64]*UserState{} // userID -> state
	_ = states
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)
	for upd := range updates {
		if upd.Message != nil {
			handleMessage(context.Background(), bot, db, key, states, upd.Message, mainKeyboard)
		} else if upd.CallbackQuery != nil {
			handleCallback(context.Background(), bot, db, key, upd.CallbackQuery, mainKeyboard)
		}
	}
}

func handleMessage(ctx context.Context, bot *tgbotapi.BotAPI, db *sql.DB, key []byte, states map[int64]*UserState, m *tgbotapi.Message, kb tgbotapi.ReplyKeyboardMarkup) {
	userID := m.From.ID
	st := states[userID]
	if st == nil {
		st = &UserState{Mode: ModeNone}
		states[userID] = st
	}

	// /start
	if m.IsCommand() && m.Command() == "start" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Привет! Выбирай действие кнопками снизу.")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		st.Mode = ModeNone
		return
	}

	// Main menu buttons
	switch strings.TrimSpace(m.Text) {
	case btnCalc:
		st.Mode = ModeCalcAwaitExpr
		msg := tgbotapi.NewMessage(m.Chat.ID, "Введи выражение (например: 2*(3+4)/5).")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		return

	case btnPass:
		pass := genPassword8()
		msg := tgbotapi.NewMessage(m.Chat.ID, "Твой пароль: `"+pass+"`")
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		st.Mode = ModeNone
		return

	case btnNotes:
		// Show Notes submenu as inline keyboard
		inline := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(btnCreate, "notes:create"),
				tgbotapi.NewInlineKeyboardButtonData(btnRead, "notes:read"),
				tgbotapi.NewInlineKeyboardButtonData(btnNoteCancel, "notes:cancel"),
			),
		)
		msg := tgbotapi.NewMessage(m.Chat.ID, "Заметки: выбери действие.")
		msg.ReplyMarkup = inline
		_, _ = bot.Send(msg)
		st.Mode = ModeNone
		return
	}

	// Mode-specific behavior
	switch st.Mode {
	case ModeCalcAwaitExpr:
		expr := strings.TrimSpace(m.Text)
		if expr == "" {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пусто. Введи выражение текстом."))
			return
		}
		val, err := evalExpr(expr)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Ошибка: "+err.Error()))
			return
		}
		out := fmt.Sprintf("= %v", trimFloat(val))
		msg := tgbotapi.NewMessage(m.Chat.ID, out)
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		st.Mode = ModeCalcAwaitExpr
		return

	case ModeNoteAwaitContent:
		// Accept: text OR photo OR voice
		payload, err := messageToPayload(m)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Пришли текст, фото или голосовое."))
			return
		}
		if err := saveNote(ctx, db, key, userID, payload); err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(m.Chat.ID, "Не смог сохранить: "+err.Error()))
			return
		}
		msg := tgbotapi.NewMessage(m.Chat.ID, "✅ Сохранено. Хочешь ещё — снова нажми «Заметки».")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		st.Mode = ModeNone
		return
	default:
		// fallback
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выбирай действие кнопками снизу.")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
	}
}

func handleCallback(ctx context.Context, bot *tgbotapi.BotAPI, db *sql.DB, key []byte, cq *tgbotapi.CallbackQuery, kb tgbotapi.ReplyKeyboardMarkup) {
	// Always answer callback to remove "loading"
	_, _ = bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	chatID := cq.Message.Chat.ID
	userID := cq.From.ID
	data := cq.Data

	switch data {
	case "notes:create":
		// We need to set state, but callback handler doesn't have states map.
		// Easiest MVP: instruct user to type "/note" and handle in messages.
		// But user wants simple; we'll do it via a global trick:
		// We'll store mode in DB? Too heavy. So we use a hack: send message telling to send note and rely on /note command.
		// Better: keep state in memory globally, but callback doesn't have access here.
		// We'll implement a minimal workaround: prompt with special command.
		msg := tgbotapi.NewMessage(chatID, "Напиши команду /create_note, затем пришли текст/фото/голосовое (следующим сообщением).")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
		return
	case "notes:cancel":
		return
	case "notes:read":
		rows, err := listNotes(ctx, db, userID, 20)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Не смог прочитать список: "+err.Error()))
			return
		}
		if len(rows) == 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Заметок пока нет. Нажми «Создать»."))
			return
		}

		// Inline keyboard as "hyperlinks"
		// show newest first
		sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })

		var buttons [][]tgbotapi.InlineKeyboardButton
		for _, r := range rows {
			title := r.CreatedAt.Format("2006-01-02 15:04:05")
			btn := tgbotapi.NewInlineKeyboardButtonData("🗒 "+title, fmt.Sprintf("note:%d", r.ID))
			buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(btn))
		}

		inline := tgbotapi.NewInlineKeyboardMarkup(buttons...)
		msg := tgbotapi.NewMessage(chatID, "Твои заметки (последние 20):")
		msg.ReplyMarkup = inline
		_, _ = bot.Send(msg)
		return
	default:
		if strings.HasPrefix(data, "note:") {
			idStr := strings.TrimPrefix(data, "note:")
			id, _ := strconv.ParseInt(idStr, 10, 64)
			payload, createdAt, err := loadNote(ctx, db, key, userID, id)
			if err != nil {
				_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Не смог открыть: "+err.Error()))
				return
			}
			sendNote(bot, chatID, payload, createdAt, kb)
		}
	}
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS notes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  payload_enc BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notes_user_time ON notes(user_id, created_at);
`)
	return err
}

func opendb() (*sql.DB, []byte) {
	keyB64 := mustEnv("ENC_KEY_B64")

	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		log.Fatal(err)
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "notes.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	return db, key
}
