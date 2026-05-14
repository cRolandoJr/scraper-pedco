package storage

import "scraper-pedco/internal/core/ports"

// Repository adapta las funciones globales del paquete a la interfaz UserRepository.
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
		out = append(out, ports.UserCredentials{ChatID: u.ChatID, User: u.User, Pass: u.Pass})
	}
	return out, nil
}
