package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"scraper-pedco/internal/core/ports"
)

var database *sql.DB

type UserData struct {
	ChatID        int64
	User          string
	Pass          string
	Session       string // token de Web Services, descifrado
	CredsRejected bool
}

func InitDB() {
	if err := loadEncryptionKey(); err != nil {
		log.Fatal("❌ Error cargando SECRET_KEY: ", err)
	}

	if err := openDatabase("./pedcobot.db"); err != nil {
		log.Fatal("❌ Error abriendo SQLite: ", err)
	}

	log.Println("✅ Base de datos SQLite lista y conectada.")
}

func openDatabase(path string) error {
	var err error
	database, err = sql.Open("sqlite3", path)
	if err != nil {
		return err
	}

	createTableStatement := `CREATE TABLE IF NOT EXISTS users (
		chat_id INTEGER PRIMARY KEY,
		pedco_user TEXT,
		pedco_pass TEXT,
		session_blob TEXT,
		creds_rejected INTEGER NOT NULL DEFAULT 0
	);`
	if _, err = database.Exec(createTableStatement); err != nil {
		return fmt.Errorf("creando tabla: %w", err)
	}
	if _, err = database.Exec(createSeenTables); err != nil {
		return fmt.Errorf("creando tablas de novedades: %w", err)
	}

	// Migración suave: agregar columna si DB vieja existía sin session_blob.
	if _, err := database.Exec(`ALTER TABLE users ADD COLUMN session_blob TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Printf("ℹ️ Migración session_blob: %v", err)
		}
	}
	if _, err := database.Exec(`ALTER TABLE users ADD COLUMN creds_rejected INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Printf("ℹ️ Migración creds_rejected: %v", err)
		}
	}
	return nil
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

	// INSERT OR REPLACE borra la fila vieja: session_blob queda NULL y
	// creds_rejected vuelve a su DEFAULT 0 (deseado: creds cambiaron).
	insertQuery := `INSERT OR REPLACE INTO users(chat_id, pedco_user, pedco_pass, session_blob) VALUES (?, ?, ?, NULL)`
	_, err = database.Exec(insertQuery, chatID, encryptedUsername, encryptedPassword)
	return err
}

// GetUser devuelve ports.ErrNoCredentials si no hay fila o el usuario está
// vacío. Un token NULL o ilegible sale vacío, sin error: el flujo cae a login.
func GetUser(chatID int64) (UserData, error) {
	var encryptedUsername, encryptedPassword, encryptedSession string
	userData := UserData{ChatID: chatID}
	err := database.QueryRow(`SELECT pedco_user, pedco_pass, COALESCE(session_blob, ''), creds_rejected FROM users WHERE chat_id = ?`, chatID).
		Scan(&encryptedUsername, &encryptedPassword, &encryptedSession, &userData.CredsRejected)
	if errors.Is(err, sql.ErrNoRows) {
		return UserData{}, ports.ErrNoCredentials
	}
	if err != nil {
		return UserData{}, err
	}

	userData.User, err = decrypt(encryptedUsername)
	if err != nil {
		return UserData{}, err
	}
	userData.Pass, err = decrypt(encryptedPassword)
	if err != nil {
		return UserData{}, err
	}
	if userData.User == "" {
		return UserData{}, ports.ErrNoCredentials
	}
	userData.Session, _ = decrypt(encryptedSession)
	return userData, nil
}

func GetAllUsers() ([]UserData, error) {
	var users []UserData

	rows, err := database.Query(`SELECT chat_id, pedco_user, pedco_pass, COALESCE(session_blob, ''), creds_rejected FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var userData UserData
		var encryptedUsername, encryptedPassword, encryptedSession string
		if err := rows.Scan(&userData.ChatID, &encryptedUsername, &encryptedPassword, &encryptedSession, &userData.CredsRejected); err != nil {
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

// SaveSession persiste el token cifrado. Se llama tras login exitoso.
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
	for _, statement := range []string{`DELETE FROM seen WHERE chat_id = ?`, `DELETE FROM seen_baseline WHERE chat_id = ?`} {
		if _, err := database.Exec(statement, chatID); err != nil {
			return err
		}
	}
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

// MarkCredentialsRejected pausa al usuario hasta el próximo /login (SaveUser).
func MarkCredentialsRejected(chatID int64) error {
	_, err := database.Exec(`UPDATE users SET creds_rejected = 1 WHERE chat_id = ?`, chatID)
	return err
}
