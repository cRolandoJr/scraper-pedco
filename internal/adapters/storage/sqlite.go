package storage

import (
	"database/sql"
	"errors"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

type UserData struct {
	ChatID  int64
	User    string
	Pass    string
	Session string // blob cifrado de cookies serializadas
}

func InitDB() {
	if err := loadKey(); err != nil {
		log.Fatal("❌ Error cargando SECRET_KEY: ", err)
	}

	var err error
	DB, err = sql.Open("sqlite3", "./pedcobot.db")
	if err != nil {
		log.Fatal("❌ Error abriendo SQLite: ", err)
	}

	crearTablaSQL := `CREATE TABLE IF NOT EXISTS users (
		chat_id INTEGER PRIMARY KEY,
		pedco_user TEXT,
		pedco_pass TEXT,
		session_blob TEXT
	);`
	if _, err = DB.Exec(crearTablaSQL); err != nil {
		log.Fatal("❌ Error creando tabla SQLite: ", err)
	}

	// Migración suave: agregar columna si DB vieja existía sin session_blob.
	if _, err := DB.Exec(`ALTER TABLE users ADD COLUMN session_blob TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Printf("ℹ️ Migración session_blob: %v", err)
		}
	}

	log.Println("✅ Base de datos SQLite lista y conectada.")
}

func SaveUser(chatID int64, user, pass string) error {
	encUser, err := encrypt(user)
	if err != nil {
		return err
	}
	encPass, err := encrypt(pass)
	if err != nil {
		return err
	}

	// INSERT OR REPLACE elimina session_blob anterior (deseado: creds cambiaron).
	query := `INSERT OR REPLACE INTO users(chat_id, pedco_user, pedco_pass, session_blob) VALUES (?, ?, ?, NULL)`
	_, err = DB.Exec(query, chatID, encUser, encPass)
	return err
}

func GetUser(chatID int64) (string, string, error) {
	var encUser, encPass string
	err := DB.QueryRow(`SELECT pedco_user, pedco_pass FROM users WHERE chat_id = ?`, chatID).
		Scan(&encUser, &encPass)
	if err != nil {
		return "", "", err
	}

	user, err := decrypt(encUser)
	if err != nil {
		return "", "", err
	}
	pass, err := decrypt(encPass)
	if err != nil {
		return "", "", err
	}
	return user, pass, nil
}

func GetAllUsers() ([]UserData, error) {
	var users []UserData

	rows, err := DB.Query(`SELECT chat_id, pedco_user, pedco_pass, COALESCE(session_blob, '') FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var u UserData
		var encUser, encPass, encSession string
		if err := rows.Scan(&u.ChatID, &encUser, &encPass, &encSession); err != nil {
			continue
		}
		u.User, err = decrypt(encUser)
		if err != nil {
			log.Printf("⚠️ Descifrado falló para ChatID %d: %v", u.ChatID, err)
			continue
		}
		u.Pass, err = decrypt(encPass)
		if err != nil {
			log.Printf("⚠️ Descifrado falló para ChatID %d: %v", u.ChatID, err)
			continue
		}
		if encSession != "" {
			u.Session, _ = decrypt(encSession) // sesión inválida ≠ fatal; se reloguea
		}
		if u.User != "" {
			users = append(users, u)
		}
	}
	return users, nil
}

// SaveSession persiste el blob de cookies cifrado. Se llama tras login exitoso.
func SaveSession(chatID int64, sessionBlob string) error {
	enc, err := encrypt(sessionBlob)
	if err != nil {
		return err
	}
	_, err = DB.Exec(`UPDATE users SET session_blob = ? WHERE chat_id = ?`, enc, chatID)
	return err
}

// ClearSession borra la sesión cached cuando expira o falla.
func ClearSession(chatID int64) error {
	_, err := DB.Exec(`UPDATE users SET session_blob = NULL WHERE chat_id = ?`, chatID)
	return err
}

func DeleteUser(chatID int64) error {
	res, err := DB.Exec(`DELETE FROM users WHERE chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("usuario no encontrado")
	}
	return nil
}
