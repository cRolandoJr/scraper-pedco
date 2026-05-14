package storage

import "scraper-pedco/internal/core/ports"

type Repository struct{}

func NewRepository() *Repository { return &Repository{} }

func (r *Repository) GetUser(chatID int64) (string, string, error) {
	return GetUser(chatID)
}

func (r *Repository) GetAllUsers() ([]ports.UserCredentials, error) {
	rows, err := GetAllUsers()
	if err != nil {
		return nil, err
	}
	out := make([]ports.UserCredentials, 0, len(rows))
	for _, u := range rows {
		out = append(out, ports.UserCredentials{
			ChatID:  u.ChatID,
			User:    u.User,
			Pass:    u.Pass,
			Session: u.Session,
		})
	}
	return out, nil
}

func (r *Repository) SaveSession(chatID int64, blob string) error {
	return SaveSession(chatID, blob)
}

func (r *Repository) ClearSession(chatID int64) error {
	return ClearSession(chatID)
}
