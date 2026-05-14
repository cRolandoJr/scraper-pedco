package ports

import (
	"errors"

	"scraper-pedco/internal/core/domain"
)

// ErrSessionExpired lo retornan los scrapers cuando la sesión cached murió.
// El service captura este sentinel para decidir si reloguear.
var ErrSessionExpired = errors.New("sesión expirada")

// Scraper define el contrato para cualquier fuente de eventos.
type Scraper interface {
	Login(user, pass string) error
	FetchEvents() ([]domain.Event, error)
	LoadSession(blob string) error
	SessionBlob() (string, error)
}

type ScraperFactory func() Scraper

// UserRepository abstrae la persistencia de credenciales y sesiones.
type UserRepository interface {
	GetUser(chatID int64) (user, pass string, err error)
	GetAllUsers() ([]UserCredentials, error)
	SaveSession(chatID int64, blob string) error
	ClearSession(chatID int64) error
}

type UserCredentials struct {
	ChatID  int64
	User    string
	Pass    string
	Session string // cookie blob cifrado-descifrado (vacío si sin caché)
}
