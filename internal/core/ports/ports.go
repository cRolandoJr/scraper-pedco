package ports

import "scraper-pedco/internal/core/domain"

// Scraper define el contrato para cualquier fuente de eventos.
// Permite cambiar Pedco por otra plataforma sin tocar el service.
type Scraper interface {
	Login(user, pass string) error
	FetchEvents() ([]domain.Event, error)
}

// ScraperFactory crea un Scraper nuevo (uno por usuario, sin estado compartido).
type ScraperFactory func() Scraper

// UserRepository abstrae la persistencia de credenciales.
type UserRepository interface {
	GetUser(chatID int64) (user, pass string, err error)
	GetAllUsers() ([]UserCredentials, error)
}

type UserCredentials struct {
	ChatID int64
	User   string
	Pass   string
}
