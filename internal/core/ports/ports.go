package ports

import (
	"errors"
	"time"

	"scraper-pedco/internal/core/domain"
)

// ErrSessionExpired: el token guardado dejó de valer (Moodle respondió
// invalidtoken o accessexception). El service lo usa para decidir si reloguear.
var ErrSessionExpired = errors.New("sesión expirada")

// ErrBadCredentials: token.php respondió invalidlogin. Solo ese errorcode.
var ErrBadCredentials = errors.New("PEDCO rechazó el usuario o la contraseña")

// ErrNoCredentials: no hay fila para el chat, o el usuario guardado está vacío.
var ErrNoCredentials = errors.New("sin credenciales guardadas")

// Source es la fuente de entregas (la API REST de Moodle).
type Source interface {
	Login(username, password string) (token string, err error)
	FetchItems(token string, now time.Time) ([]domain.Item, error)
}

// UserRepository abstrae la persistencia de credenciales y sesiones.
type UserRepository interface {
	GetUser(chatID int64) (UserCredentials, error)
	GetAllUsers() ([]UserCredentials, error)
	SaveUser(chatID int64, username, password string) error
	SaveSession(chatID int64, token string) error
	ClearSession(chatID int64) error
	MarkCredentialsRejected(chatID int64) error
}

type UserCredentials struct {
	ChatID        int64
	User          string
	Pass          string
	Session       string // token de Web Services descifrado (vacío si no hay o no se pudo descifrar)
	CredsRejected bool
}
