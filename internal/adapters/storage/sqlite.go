package storage

import (
	"database/sql"
	"log"

	// El guion bajo (_) significa que importamos el paquete para que se ejecute su función init()
	// y registre el driver de sqlite3, pero no lo llamaremos directamente en el código.
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// InitDB crea la conexión y la tabla si no existe
func InitDB() {
	var err error
	// Abre o crea un archivo llamado pedcobot.db en la raíz del proyecto
	DB, err = sql.Open("sqlite3", "./pedcobot.db")
	if err != nil {
		log.Fatal("❌ Error abriendo SQLite: ", err)
	}

	// Creamos la tabla. Usamos chat_id como llave primaria para que no haya duplicados.
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

// SaveUser inserta un usuario nuevo o actualiza su contraseña si ya existe
func SaveUser(chatID int64, user, pass string) error {
	// INSERT OR REPLACE es magia de SQLite: Si el chat_id ya existe, lo sobrescribe.
	query := `INSERT OR REPLACE INTO users(chat_id, pedco_user, pedco_pass) VALUES (?, ?, ?)`

	statement, err := DB.Prepare(query)
	if err != nil {
		return err
	}
	defer statement.Close()

	_, err = statement.Exec(chatID, user, pass)
	return err
}

// GetUser buscará las credenciales cuando el bot necesite hacer scraping (lo usaremos luego)
func GetUser(chatID int64) (string, string, error) {
	var user, pass string
	query := `SELECT pedco_user, pedco_pass FROM users WHERE chat_id = ?`

	err := DB.QueryRow(query, chatID).Scan(&user, &pass)
	if err != nil {
		return "", "", err
	}
	return user, pass, nil
}
