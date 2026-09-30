package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"scraper-pedco/internal/core/ports"
)

// openTestDatabase abre una base nueva en un archivo temporal (nunca pedcobot.db).
func openTestDatabase(t *testing.T, path string) {
	t.Helper()
	encryptionKey = []byte("0123456789abcdef0123456789abcdef")
	if err := openDatabase(path); err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	t.Cleanup(func() { database.Close() })
}

// C24: SaveUser sobre un usuario con el flag puesto → flag en 0.
func TestSaveUserResetsCredentialsRejected(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if err := SaveUser(1, "ana", "vieja"); err != nil {
		t.Fatal(err)
	}
	if err := MarkCredentialsRejected(1); err != nil {
		t.Fatal(err)
	}
	flagged, err := GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	if !flagged.CredsRejected {
		t.Fatalf("control positivo: el flag debía quedar puesto")
	}
	all, err := GetAllUsers()
	if err != nil || len(all) != 1 || !all[0].CredsRejected {
		t.Fatalf("GetAllUsers no devuelve el flag: %+v, %v", all, err)
	}

	if err := SaveUser(1, "ana", "nueva"); err != nil {
		t.Fatal(err)
	}
	reset, err := GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	if reset.CredsRejected || reset.Pass != "nueva" {
		t.Errorf("tras SaveUser: %+v, quiero flag en 0 y la clave nueva", reset)
	}
}

// C25: una base vieja sin la columna arranca bien y queda con creds_rejected = 0.
func TestOldDatabaseGetsColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	encryptionKey = []byte("0123456789abcdef0123456789abcdef")
	encryptedUser, _ := encrypt("ana")
	encryptedPass, _ := encrypt("clave")
	oldDatabase, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldDatabase.Exec(`CREATE TABLE users (chat_id INTEGER PRIMARY KEY, pedco_user TEXT, pedco_pass TEXT, session_blob TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDatabase.Exec(`INSERT INTO users VALUES (1, ?, ?, NULL)`, encryptedUser, encryptedPass); err != nil {
		t.Fatal(err)
	}
	oldDatabase.Close()

	openTestDatabase(t, path)

	var flag int
	if err := database.QueryRow(`SELECT creds_rejected FROM users WHERE chat_id = 1`).Scan(&flag); err != nil {
		t.Fatalf("la columna no existe tras abrir: %v", err)
	}
	if flag != 0 {
		t.Errorf("creds_rejected = %d, quiero 0", flag)
	}
	user, err := GetUser(1)
	if err != nil || user.User != "ana" || user.CredsRejected {
		t.Errorf("GetUser en base migrada = %+v, %v", user, err)
	}
	// Reabrir la misma base (la columna ya existe) también arranca.
	database.Close()
	openTestDatabase(t, path)
}

// GetUser: sin fila → ErrNoCredentials; token NULL o ilegible → vacío sin error.
func TestGetUserContract(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if _, err := GetUser(99); !errors.Is(err, ports.ErrNoCredentials) {
		t.Errorf("sin fila: err = %v, quiero ErrNoCredentials", err)
	}

	if err := SaveUser(1, "ana", "clave"); err != nil {
		t.Fatal(err)
	}
	if user, err := GetUser(1); err != nil || user.Session != "" {
		t.Errorf("token NULL: %+v, %v", user, err)
	}
	if err := SaveSession(1, "tok-123"); err != nil {
		t.Fatal(err)
	}
	if user, err := GetUser(1); err != nil || user.Session != "tok-123" {
		t.Errorf("token guardado: %+v, %v", user, err)
	}
	if _, err := database.Exec(`UPDATE users SET session_blob = 'no-es-base64!' WHERE chat_id = 1`); err != nil {
		t.Fatal(err)
	}
	if user, err := GetUser(1); err != nil || user.Session != "" || user.User != "ana" {
		t.Errorf("token ilegible: %+v, %v", user, err)
	}

	if err := SaveUser(2, "", "clave"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetUser(2); !errors.Is(err, ports.ErrNoCredentials) {
		t.Errorf("usuario vacío: err = %v, quiero ErrNoCredentials", err)
	}
}
