package storage

import (
	"database/sql"
	"errors"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var database *sql.DB

type UserData struct {
	ChatID  int64
	User    string
	Pass    string
	Session string // blob cifrado de cookies serializadas
}

func InitDB() {
	if err := loadEncryptionKey(); err != nil {
		log.Fatal("❌ Error cargando SECRET_KEY: ", err)
	}

	var err error
	database, err = sql.Open("sqlite3", "./pedcobot.db")
	if err != nil {
		log.Fatal("❌ Error abriendo SQLite: ", err)
	}

	createTableStatement := `CREATE TABLE IF NOT EXISTS users (
		chat_id INTEGER PRIMARY KEY,
		pedco_user TEXT,
		pedco_pass TEXT,
		session_blob TEXT
	);`
	if _, err = database.Exec(createTableStatement); err != nil {
		log.Fatal("❌ Error creando tabla SQLite: ", err)
	}

	// Migración suave: agregar columna si DB vieja existía sin session_blob.
	if _, err := database.Exec(`ALTER TABLE users ADD COLUMN session_blob TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Printf("ℹ️ Migración session_blob: %v", err)
		}
	}

	log.Println("✅ Base de datos SQLite lista y conectada.")
}

func SaveUser(chatID int64, username, password string) error {
	encryptedUsername, err := encrypt(username)
	if err != nil {
		return err
	}
	encryptedPassword, err := encrypt(password)
	if err != nil {
		return err
	}

	// INSERT OR REPLACE elimina session_blob anterior (deseado: creds cambiaron).
	insertQuery := `INSERT OR REPLACE INTO users(chat_id, pedco_user, pedco_pass, session_blob) VALUES (?, ?, ?, NULL)`
	_, err = database.Exec(insertQuery, chatID, encryptedUsername, encryptedPassword)
	return err
}

func GetUser(chatID int64) (string, string, error) {
	var encryptedUsername, encryptedPassword string
	err := database.QueryRow(`SELECT pedco_user, pedco_pass FROM users WHERE chat_id = ?`, chatID).
		Scan(&encryptedUsername, &encryptedPassword)
	if err != nil {
		return "", "", err
	}

	username, err := decrypt(encryptedUsername)
	if err != nil {
		return "", "", err
	}
	password, err := decrypt(encryptedPassword)
	if err != nil {
		return "", "", err
	}
	return username, password, nil
}

func GetAllUsers() ([]UserData, error) {
	var users []UserData

	rows, err := database.Query(`SELECT chat_id, pedco_user, pedco_pass, COALESCE(session_blob, '') FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var userData UserData
		var encryptedUsername, encryptedPassword, encryptedSession string
		if err := rows.Scan(&userData.ChatID, &encryptedUsername, &encryptedPassword, &encryptedSession); err != nil {
			continue
		}
		userData.User, err = decrypt(encryptedUsername)
		if err != nil {
			log.Printf("⚠️ Descifrado falló para ChatID %d: %v", userData.ChatID, err)
			continue
		}
		userData.Pass, err = decrypt(encryptedPassword)
		if err != nil {
			log.Printf("⚠️ Descifrado falló para ChatID %d: %v", userData.ChatID, err)
			continue
		}
		if encryptedSession != "" {
			userData.Session, _ = decrypt(encryptedSession) // sesión inválida ≠ fatal; se reloguea
		}
		if userData.User != "" {
			users = append(users, userData)
		}
	}
	return users, nil
}

// SaveSession persiste el blob de cookies cifrado. Se llama tras login exitoso.
func SaveSession(chatID int64, sessionBlob string) error {
	encryptedSession, err := encrypt(sessionBlob)
	if err != nil {
		return err
	}
	_, err = database.Exec(`UPDATE users SET session_blob = ? WHERE chat_id = ?`, encryptedSession, chatID)
	return err
}

// ClearSession borra la sesión cached cuando expira o falla.
func ClearSession(chatID int64) error {
	_, err := database.Exec(`UPDATE users SET session_blob = NULL WHERE chat_id = ?`, chatID)
	return err
}

func DeleteUser(chatID int64) error {
	result, err := database.Exec(`DELETE FROM users WHERE chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	rowsDeleted, _ := result.RowsAffected()
	if rowsDeleted == 0 {
		return errors.New("usuario no encontrado")
	}
	return nil
}
