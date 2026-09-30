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

// ErrSendPermanent: Telegram rechazó el envío por su contenido (4xx salvo 401 y 429).
// Reintentar el mismo envío no sirve.
var ErrSendPermanent = errors.New("Telegram rechazó el envío")

// ErrSendUnauthorized: Telegram respondió 401 (token del bot inválido o revocado).
// Falla todo envío, no ese mensaje: no se marca nada y se corta la ronda del usuario.
var ErrSendUnauthorized = errors.New("Telegram rechazó el token del bot")

// Channel es a qué tema del chat va un mensaje; el adaptador de Telegram lo traduce.
type Channel string

const (
	ChannelDeliveries Channel = "entregas"
	ChannelNews       Channel = "novedades"
	ChannelGeneral    Channel = "" // fuera de tema
)

// Source es la fuente de entregas y novedades (la API REST de Moodle).
type Source interface {
	Login(username, password string) (token string, err error)
	FetchItems(token string, now time.Time) ([]domain.Item, error)
	Profile(token string) (userID int, courses []domain.Course, err error)
	// Forums trae los avisos de los foros news, por curso; un curso que falló va en el mapa de errores.
	Forums(token string, courses []domain.Course) (map[int][]domain.ForumPost, map[int]error)
	Grades(token string, courseID, userID int) ([]domain.GradeItem, error)
	// Materials devuelve solo resource, url y folder con uservisible.
	Materials(token string, courseID int) ([]domain.Material, error)
}

// SeenRepository recuerda qué novedades ya se avisaron y qué cursos tienen línea de base.
type SeenRepository interface {
	HasBaseline(chatID int64, kind string, courseID int) (bool, error)
	SetBaseline(chatID int64, kind string, courseID int) error
	IsSeen(chatID int64, kind, key string) (bool, error)
	MarkSeen(chatID int64, kind, key string) error
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
