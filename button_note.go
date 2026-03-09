package main

// ---------- Commands workaround for create_note ----------
// Add this to message handler: command /create_note sets mode.
// For simplicity, we parse it here in messageToPayload flow by intercepting in handleMessage.
// To keep single-file easy, we do it with a helper:
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func messageToPayload(m *tgbotapi.Message) (NotePayload, error) {
	if m.Voice != nil {
		return NotePayload{Kind: "voice", FileID: m.Voice.FileID}, nil
	}
	if len(m.Photo) > 0 {
		best := m.Photo[len(m.Photo)-1] // largest
		return NotePayload{Kind: "photo", FileID: best.FileID, Caption: m.Caption}, nil
	}
	if strings.TrimSpace(m.Text) != "" {
		return NotePayload{Kind: "text", Text: m.Text}, nil
	}
	return NotePayload{}, errors.New("unsupported")
}

func sendNote(bot *tgbotapi.BotAPI, chatID int64, p NotePayload, createdAt time.Time, kb tgbotapi.ReplyKeyboardMarkup) {
	header := "🗒 " + createdAt.Format("2006-01-02 15:04:05")

	switch p.Kind {
	case "text":
		msg := tgbotapi.NewMessage(chatID, header+"\n\n"+p.Text)
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
	case "photo":
		pc := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(p.FileID))
		if strings.TrimSpace(p.Caption) != "" {
			pc.Caption = header + "\n" + p.Caption
		} else {
			pc.Caption = header
		}
		pc.ReplyMarkup = kb
		_, _ = bot.Send(pc)
	case "voice":
		vc := tgbotapi.NewVoice(chatID, tgbotapi.FileID(p.FileID))
		vc.Caption = header
		vc.ReplyMarkup = kb
		_, _ = bot.Send(vc)
	default:
		msg := tgbotapi.NewMessage(chatID, header+"\n\n(неизвестный тип заметки)")
		msg.ReplyMarkup = kb
		_, _ = bot.Send(msg)
	}
}

func loadNote(ctx context.Context, db *sql.DB, key []byte, userID, noteID int64) (NotePayload, time.Time, error) {
	var enc []byte
	var tStr string
	err := db.QueryRowContext(ctx,
		`SELECT created_at, payload_enc FROM notes WHERE id=? AND user_id=?`,
		noteID, userID,
	).Scan(&tStr, &enc)
	if err != nil {
		return NotePayload{}, time.Time{}, err
	}
	raw, err := decryptAESGCM(key, enc)
	if err != nil {
		return NotePayload{}, time.Time{}, err
	}
	var p NotePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return NotePayload{}, time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, tStr)
	return p, t.Local(), nil
}

func listNotes(ctx context.Context, db *sql.DB, userID int64, limit int) ([]NoteRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, created_at FROM notes WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NoteRow
	for rows.Next() {
		var id int64
		var tStr string
		if err := rows.Scan(&id, &tStr); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, tStr)
		out = append(out, NoteRow{ID: id, CreatedAt: t.Local()})
	}
	return out, rows.Err()
}

func saveNote(ctx context.Context, db *sql.DB, key []byte, userID int64, payload NotePayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	enc, err := encryptAESGCM(key, raw)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO notes(user_id, created_at, payload_enc) VALUES(?,?,?)`,
		userID, time.Now().UTC().Format(time.RFC3339Nano), enc,
	)
	return err
}
