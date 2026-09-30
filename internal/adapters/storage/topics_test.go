package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	"scraper-pedco/internal/core/ports"
)

// T9: una base vieja (sin topics) arranca y crea la tabla.
func TestOldDatabaseGetsTopicsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	oldDatabase, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldDatabase.Exec(`CREATE TABLE users (chat_id INTEGER PRIMARY KEY, pedco_user TEXT, pedco_pass TEXT, session_blob TEXT)`); err != nil {
		t.Fatal(err)
	}
	oldDatabase.Close()

	openTestDatabase(t, path)

	var tables int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'topics'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("tabla topics tras abrir la base vieja: %d, %v", tables, err)
	}
	if _, found, err := TopicID(1, ports.ChannelDeliveries); err != nil || found {
		t.Errorf("base vieja sin temas: found=%v err=%v", found, err)
	}
}

// Contrato: INSERT OR IGNORE no pisa, Forget borra solo ese canal.
func TestTopicsContract(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if err := SaveTopic(1, ports.ChannelDeliveries, 11); err != nil {
		t.Fatal(err)
	}
	if err := SaveTopic(1, ports.ChannelDeliveries, 99); err != nil {
		t.Fatal(err)
	}
	if err := SaveTopic(1, ports.ChannelNews, 22); err != nil {
		t.Fatal(err)
	}
	if threadID, found, err := TopicID(1, ports.ChannelDeliveries); err != nil || !found || threadID != 11 {
		t.Errorf("el segundo SaveTopic no pisa: %d %v %v", threadID, found, err)
	}
	if err := ForgetTopic(1, ports.ChannelDeliveries); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := TopicID(1, ports.ChannelDeliveries); found {
		t.Errorf("Forget no borró Entregas")
	}
	if threadID, found, _ := TopicID(1, ports.ChannelNews); !found || threadID != 22 {
		t.Errorf("Forget de Entregas borró Novedades: %d %v", threadID, found)
	}
}

// T8 (persistencia): SaveUser no toca los temas; DeleteUser los borra.
func TestTopicsSurviveSaveUserAndDieWithDeleteUser(t *testing.T) {
	openTestDatabase(t, filepath.Join(t.TempDir(), "test.db"))

	if err := SaveUser(1, "ana", "clave"); err != nil {
		t.Fatal(err)
	}
	if err := SaveTopic(1, ports.ChannelDeliveries, 11); err != nil {
		t.Fatal(err)
	}
	if err := SaveTopic(2, ports.ChannelDeliveries, 33); err != nil {
		t.Fatal(err)
	}
	if err := SaveUser(1, "ana", "nueva"); err != nil {
		t.Fatal(err)
	}
	if threadID, found, err := TopicID(1, ports.ChannelDeliveries); err != nil || !found || threadID != 11 {
		t.Fatalf("SaveUser borró el tema: %d %v %v", threadID, found, err)
	}
	if err := DeleteUser(1); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := TopicID(1, ports.ChannelDeliveries); found {
		t.Errorf("DeleteUser no borró el tema")
	}
	if _, found, _ := TopicID(2, ports.ChannelDeliveries); !found {
		t.Errorf("DeleteUser borró el tema de otro chat")
	}
}
