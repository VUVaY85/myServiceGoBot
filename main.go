package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	_ "modernc.org/sqlite"
)

const (
	btnCalc   = "🧮 Калькулятор"
	btnPass   = "🔐 Генератор паролей"
	btnNotes  = "📝 Заметки"
	btnCreate = "➕ Создать"
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env %s not set", key)
	}
	return v
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	token := mustEnv("BOT_TOKEN")
	keyB64 := mustEnv("ENC_KEY_B64")
	dbPath := os.Getenv("DB_PATH")

	fmt.Println(token)
	fmt.Println(keyB64)
	fmt.Println(dbPath)
}
