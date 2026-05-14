package storage

import "scraper-pedco/internal/core/ports"

type Repository struct{}

func NewRepository() *Repository { return &Repository{} }

func (repository *Repository) GetUser(chatID int64) (string, string, error) {
	return GetUser(chatID)
}

func (repository *Repository) GetAllUsers() ([]ports.UserCredentials, error) {
	allUsers, err := GetAllUsers()
	if err != nil {
		return nil, err
	}
	credentials := make([]ports.UserCredentials, 0, len(allUsers))
	for _, userData := range allUsers {
		credentials = append(credentials, ports.UserCredentials{
			ChatID:  userData.ChatID,
			User:    userData.User,
			Pass:    userData.Pass,
			Session: userData.Session,
		})
	}
	return credentials, nil
}

func (repository *Repository) SaveSession(chatID int64, sessionBlob string) error {
	return SaveSession(chatID, sessionBlob)
}

func (repository *Repository) ClearSession(chatID int64) error {
	return ClearSession(chatID)
}
