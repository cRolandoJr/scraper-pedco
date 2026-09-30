package storage

// Tablas de novedades: kind es forum, grade o material.
const createSeenTables = `CREATE TABLE IF NOT EXISTS seen (
	chat_id INTEGER, kind TEXT, key TEXT, PRIMARY KEY(chat_id, kind, key)
);
CREATE TABLE IF NOT EXISTS seen_baseline (
	chat_id INTEGER, kind TEXT, course_id INTEGER, PRIMARY KEY(chat_id, kind, course_id)
);`

func HasBaseline(chatID int64, kind string, courseID int) (bool, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM seen_baseline WHERE chat_id = ? AND kind = ? AND course_id = ?`, chatID, kind, courseID).Scan(&count)
	return count > 0, err
}

func SetBaseline(chatID int64, kind string, courseID int) error {
	_, err := database.Exec(`INSERT OR IGNORE INTO seen_baseline(chat_id, kind, course_id) VALUES (?, ?, ?)`, chatID, kind, courseID)
	return err
}

func IsSeen(chatID int64, kind, key string) (bool, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM seen WHERE chat_id = ? AND kind = ? AND key = ?`, chatID, kind, key).Scan(&count)
	return count > 0, err
}

func MarkSeen(chatID int64, kind, key string) error {
	_, err := database.Exec(`INSERT OR IGNORE INTO seen(chat_id, kind, key) VALUES (?, ?, ?)`, chatID, kind, key)
	return err
}

func (repository *Repository) HasBaseline(chatID int64, kind string, courseID int) (bool, error) {
	return HasBaseline(chatID, kind, courseID)
}

func (repository *Repository) SetBaseline(chatID int64, kind string, courseID int) error {
	return SetBaseline(chatID, kind, courseID)
}

func (repository *Repository) IsSeen(chatID int64, kind, key string) (bool, error) {
	return IsSeen(chatID, kind, key)
}

func (repository *Repository) MarkSeen(chatID int64, kind, key string) error {
	return MarkSeen(chatID, kind, key)
}
