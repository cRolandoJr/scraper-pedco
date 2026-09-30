package storage

import (
	"database/sql"
	"errors"

	"scraper-pedco/internal/core/ports"
)

// Temas de Telegram por (chat, canal). Tabla aparte de users: SaveUser hace INSERT OR REPLACE.
const createTopicsTable = `CREATE TABLE IF NOT EXISTS topics (
    chat_id INTEGER, channel TEXT, thread_id INTEGER, PRIMARY KEY(chat_id, channel)
);`

func TopicID(chatID int64, channel ports.Channel) (threadID int, found bool, err error) {
	err = database.QueryRow(`SELECT thread_id FROM topics WHERE chat_id = ? AND channel = ?`, chatID, string(channel)).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return threadID, true, nil
}

// SaveTopic no pisa un tema ya guardado: quien llama relee el id.
func SaveTopic(chatID int64, channel ports.Channel, threadID int) error {
	_, err := database.Exec(`INSERT OR IGNORE INTO topics(chat_id, channel, thread_id) VALUES (?, ?, ?)`, chatID, string(channel), threadID)
	return err
}

func ForgetTopic(chatID int64, channel ports.Channel) error {
	_, err := database.Exec(`DELETE FROM topics WHERE chat_id = ? AND channel = ?`, chatID, string(channel))
	return err
}

func (repository *Repository) TopicID(chatID int64, channel ports.Channel) (int, bool, error) {
	return TopicID(chatID, channel)
}

func (repository *Repository) SaveTopic(chatID int64, channel ports.Channel, threadID int) error {
	return SaveTopic(chatID, channel, threadID)
}

func (repository *Repository) ForgetTopic(chatID int64, channel ports.Channel) error {
	return ForgetTopic(chatID, channel)
}
