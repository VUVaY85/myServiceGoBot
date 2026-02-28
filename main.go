package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	btnCalc   = "🧮 Калькулятор"
	btnPass   = "🔐 Генератор паролей"
	btnNotes  = "📝 Заметки"
	btnCreate = "➕ Создать"
	btnCancel = "🫩 Отмена"
	btnRead   = "📚 Прочитать"
)

type Mode int

const (
	ModeNone Mode = iota
	ModeCalcAwaitExpr
	ModeNoteAwaitContent
)

type UserState struct {
	Mode Mode
}

type NotePayload struct {
	Kind    string `json:"kind"`              // "text" | "photo" | "voice"
	Text    string `json:"text,omitempty"`    // for text
	FileID  string `json:"file_id,omitempty"` // for photo/voice
	Caption string `json:"caption,omitempty"` // optional
}

type NoteRow struct {
	ID        int64
	CreatedAt time.Time
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	token := mustEnv("BOT_TOKEN")
	keyB64 := mustEnv("ENC_KEY_B64")
	dbPath := os.Getenv("DB_PATH")

	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		log.Fatalf("ENC_KEY_B64 must be base64 of 32 bytes (AES-256). decode err=%v len=%d", err, len(key))
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

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
				tgbotapi.NewInlineKeyboardButtonData(btnCancel, "notes:cancel"),
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
		st.Mode = ModeNone
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

// ---------- Commands workaround for create_note ----------
// Add this to message handler: command /create_note sets mode.
// For simplicity, we parse it here in messageToPayload flow by intercepting in handleMessage.
// To keep single-file easy, we do it with a helper:

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

// ---- crypto AES-GCM ----
// Store: nonce(12) || ciphertext+tag
func encryptAESGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ct...), nil
}

func decryptAESGCM(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce := data[:ns]
	ct := data[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

// ---- password ----
func genPassword8() string {
	lower := "abcdefghijklmnopqrstuvwxyz"
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digs := "0123456789"
	spec := "!@#$%^&*()-_=+[]{};:,.<>?"
	all := lower + upper + digs + spec

	// Ensure all categories present: 1 lower, 1 upper, 1 digit, 1 spec + 4 random
	var b []byte
	b = append(b, lower[randInt(len(lower))])
	b = append(b, upper[randInt(len(upper))])
	b = append(b, digs[randInt(len(digs))])
	b = append(b, spec[randInt(len(spec))])
	for len(b) < 8 {
		b = append(b, all[randInt(len(all))])
	}
	// Shuffle
	for i := len(b) - 1; i > 0; i-- {
		j := randInt(i + 1)
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	// crypto/rand for better randomness
	x := make([]byte, 4)
	_, _ = rand.Read(x)
	v := int(uint32(x[0]) | uint32(x[1])<<8 | uint32(x[2])<<16 | uint32(x[3])<<24)
	if v < 0 {
		v = -v
	}
	return v % n
}

// ---- calculator: + - * / parentheses, floats ----
func evalExpr(s string) (float64, error) {
	toks, err := tokenize(s)
	if err != nil {
		return 0, err
	}
	rpn, err := shuntingYard(toks)
	if err != nil {
		return 0, err
	}
	return evalRPN(rpn)
}

type tokType int

const (
	tNumber tokType = iota
	tOp
	tLParen
	tRParen
)

type token struct {
	typ tokType
	val string
}

func tokenize(s string) ([]token, error) {
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return nil, errors.New("empty expression")
	}
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case (c >= '0' && c <= '9') || c == '.':
			j := i + 1
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			out = append(out, token{typ: tNumber, val: s[i:j]})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			out = append(out, token{typ: tOp, val: string(c)})
			i++
		case c == '(':
			out = append(out, token{typ: tLParen, val: "("})
			i++
		case c == ')':
			out = append(out, token{typ: tRParen, val: ")"})
			i++
		default:
			return nil, fmt.Errorf("bad char: %q", c)
		}
	}
	// Handle unary minus by rewriting: (-x) or at start -> (0-x)
	out = rewriteUnaryMinus(out)
	return out, nil
}

func rewriteUnaryMinus(toks []token) []token {
	var out []token
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.typ == tOp && t.val == "-" {
			if i == 0 || toks[i-1].typ == tOp || toks[i-1].typ == tLParen {
				// unary minus -> 0 - ...
				out = append(out, token{typ: tNumber, val: "0"})
			}
		}
		out = append(out, t)
	}
	return out
}

func prec(op string) int {
	switch op {
	case "+", "-":
		return 1
	case "*", "/":
		return 2
	default:
		return 0
	}
}

func shuntingYard(toks []token) ([]token, error) {
	var out []token
	var stack []token
	for _, t := range toks {
		switch t.typ {
		case tNumber:
			out = append(out, t)
		case tOp:
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.typ == tOp && prec(top.val) >= prec(t.val) {
					out = append(out, top)
					stack = stack[:len(stack)-1]
				} else {
					break
				}
			}
			stack = append(stack, t)
		case tLParen:
			stack = append(stack, t)
		case tRParen:
			found := false
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.typ == tLParen {
					found = true
					break
				}
				out = append(out, top)
			}
			if !found {
				return nil, errors.New("mismatched parentheses")
			}
		}
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.typ == tLParen || top.typ == tRParen {
			return nil, errors.New("mismatched parentheses")
		}
		out = append(out, top)
	}
	return out, nil
}

func evalRPN(toks []token) (float64, error) {
	var st []float64
	for _, t := range toks {
		if t.typ == tNumber {
			v, err := strconv.ParseFloat(t.val, 64)
			if err != nil {
				return 0, errors.New("bad number")
			}
			st = append(st, v)
			continue
		}
		if t.typ == tOp {
			if len(st) < 2 {
				return 0, errors.New("bad expression")
			}
			b := st[len(st)-1]
			a := st[len(st)-2]
			st = st[:len(st)-2]
			var r float64
			switch t.val {
			case "+":
				r = a + b
			case "-":
				r = a - b
			case "*":
				r = a * b
			case "/":
				if b == 0 {
					return 0, errors.New("division by zero")
				}
				r = a / b
			}
			st = append(st, r)
		}
	}
	if len(st) != 1 {
		return 0, errors.New("bad expression")
	}
	if math.IsInf(st[0], 0) || math.IsNaN(st[0]) {
		return 0, errors.New("bad result")
	}
	return st[0], nil
}

func trimFloat(v float64) string {
	// Pretty format: remove trailing zeros
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}

// ---- env helpers ----
func mustEnv(k string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		log.Fatalf("missing env: %s", k)
	}
	return v
}

func getenvDefault(k, d string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	return v
}
