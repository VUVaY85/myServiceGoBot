package main

import "time"

const (
	btnCalc       = "🧮 Калькулятор"
	btnPass       = "🔐 Генератор паролей"
	btnNotes      = "📝 Заметки"
	btnCreate     = "➕ Создать"
	btnNoteCancel = "🫩 Отмена"
	btnCalcCancel = "🫩 Отмена"
	btnRead       = "📚 Прочитать"
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
