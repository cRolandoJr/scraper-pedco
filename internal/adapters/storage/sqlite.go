package storage

import (
	"database/sql"
	"errors"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

type UserData struct {
	ChatID int64
	User   string
	Pass   string
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
		pedco_pass TEXT
	);`

	_, err = DB.Exec(crearTablaSQL)
	if err != nil {
		log.Fatal("❌ Error creando tabla SQLite: ", err)
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

	query := `INSERT OR REPLACE INTO users(chat_id, pedco_user, pedco_pass) VALUES (?, ?, ?)`
	stmt, err := DB.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(chatID, encUser, encPass)
	return err
}

func GetUser(chatID int64) (string, string, error) {
	var encUser, encPass string
	query := `SELECT pedco_user, pedco_pass FROM users WHERE chat_id = ?`

	err := DB.QueryRow(query, chatID).Scan(&encUser, &encPass)
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

	rows, err := DB.Query(`SELECT chat_id, pedco_user, pedco_pass FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var u UserData
		var encUser, encPass string
		if err := rows.Scan(&u.ChatID, &encUser, &encPass); err != nil {
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
		if u.User != "" {
			users = append(users, u)
		}
	}
	return users, nil
}

// DeleteUser elimina la fila completa del usuario.
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
