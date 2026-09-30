package storage

import "scraper-pedco/internal/core/ports"

type Repository struct{}

func NewRepository() *Repository { return &Repository{} }

func (repository *Repository) GetUser(chatID int64) (ports.UserCredentials, error) {
	userData, err := GetUser(chatID)
	if err != nil {
		return ports.UserCredentials{}, err
	}
	return toCredentials(userData), nil
}

func (repository *Repository) GetAllUsers() ([]ports.UserCredentials, error) {
	allUsers, err := GetAllUsers()
	if err != nil {
		return nil, err
	}
	credentials := make([]ports.UserCredentials, 0, len(allUsers))
	for _, userData := range allUsers {
		credentials = append(credentials, toCredentials(userData))
	}
	return credentials, nil
}

func (repository *Repository) SaveUser(chatID int64, username, password string) error {
	return SaveUser(chatID, username, password)
}

func (repository *Repository) SaveSession(chatID int64, token string) error {
	return SaveSession(chatID, token)
}

func (repository *Repository) ClearSession(chatID int64) error {
	return ClearSession(chatID)
}

func (repository *Repository) MarkCredentialsRejected(chatID int64) error {
	return MarkCredentialsRejected(chatID)
}

func toCredentials(userData UserData) ports.UserCredentials {
	return ports.UserCredentials{
		ChatID:        userData.ChatID,
		User:          userData.User,
		Pass:          userData.Pass,
		Session:       userData.Session,
		CredsRejected: userData.CredsRejected,
	}
}
