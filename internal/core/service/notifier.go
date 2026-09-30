package service

import (
	"errors"
	"fmt"
	"log"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

const (
	credentialsRejectedMessage = "🔑 PEDCO rechazó tu usuario o contraseña guardados (¿la cambiaste?). Mandá /login para actualizarlos. Hasta entonces no te llegan avisos."
	noCredentialsMessage       = "❌ No tienes credenciales válidas. Usa /login."
	pedcoUnavailableMessage    = "⚠️ No pude consultar tus entregas ahora (PEDCO puede estar caído). Probá en un rato."
	nothingPendingMessage      = "✅ ¡No tienes entregas pendientes! Relájate."
	saveFailedMessage          = "❌ Hubo un error guardando tus datos."
	loginOKMessage             = "🔐 Listo, entré a PEDCO con tu cuenta. Usá /tps para ver tus entregas."
	loginRejectedMessage       = "❌ PEDCO rechazó ese usuario o contraseña. Probá /login de nuevo."
	loginUnreachableMessage    = "💾 Guardé tus datos, pero PEDCO no responde ahora; los pruebo en la próxima ronda."
)

// MessageSender manda a Telegram. Send va en Markdown legacy; SendPlain sin
// parse mode. Un error con errors.Is(err, ports.ErrSendPermanent) no se reintenta.
// channel elige el tema del chat.
type MessageSender interface {
	Send(chatID int64, channel ports.Channel, message string) error
	SendPlain(chatID int64, channel ports.Channel, message string) error
}

type Notifier struct {
	userRepository ports.UserRepository
	seenRepository ports.SeenRepository
	source         ports.Source
	messageSender  MessageSender
	location       *time.Location
	now            func() time.Time
	delayBetween   time.Duration
	signatureBlock string
}

func NewNotifier(userRepository ports.UserRepository, seenRepository ports.SeenRepository, source ports.Source, messageSender MessageSender, location *time.Location) *Notifier {
	return &Notifier{
		userRepository: userRepository,
		seenRepository: seenRepository,
		source:         source,
		messageSender:  messageSender,
		location:       location,
		now:            time.Now,
		delayBetween:   3 * time.Second,
		signatureBlock: "---\n👨‍💻 *PedcoBot* | " +
			"[Rolando Cobis](https://linkedin.com/in/rolando-cobis-jr)",
	}
}

func (notifier *Notifier) NotifyAll() {
	log.Println("🤖 Iniciando ronda de revisión automática...")
	allUsers, err := notifier.userRepository.GetAllUsers()
	if err != nil {
		log.Println("❌ Error obteniendo usuarios:", err)
		return
	}

	for _, userCredentials := range allUsers {
		if userCredentials.CredsRejected {
			log.Printf("⏸️ ChatID %d: credenciales rechazadas, en pausa hasta /login", userCredentials.ChatID)
			continue
		}
		notifier.notifyUser(userCredentials)
		time.Sleep(notifier.delayBetween)
	}
	log.Println("🏁 Ronda finalizada.")
}

func (notifier *Notifier) notifyUser(userCredentials ports.UserCredentials) {
	chatID := userCredentials.ChatID
	items, token, err := notifier.fetchItemsFor(userCredentials)
	if errors.Is(err, ports.ErrBadCredentials) {
		notifier.markRejected(chatID)
		if sendErr := notifier.messageSender.Send(chatID, ports.ChannelGeneral, credentialsRejectedMessage); sendErr != nil {
			log.Printf("⚠️ Falló envío del aviso de credenciales a ChatID %d: %v", chatID, sendErr)
		}
		return
	}
	if err != nil {
		log.Printf("⚠️ ChatID %d: %v", chatID, err)
		return
	}
	if hasPending(items) {
		if err := notifier.messageSender.Send(chatID, ports.ChannelDeliveries, notifier.formatItems(items, true)); err != nil {
			log.Printf("⚠️ Falló envío ChatID %d: %v", chatID, err)
		} else {
			log.Printf("✅ Notificación enviada a ChatID %d", chatID)
		}
	}
	notifier.notifyNovelties(chatID, token)
}

// NotifyOne arma la respuesta de /tps.
func (notifier *Notifier) NotifyOne(chatID int64) string {
	userCredentials, err := notifier.userRepository.GetUser(chatID)
	if err != nil {
		if !errors.Is(err, ports.ErrNoCredentials) {
			log.Printf("⚠️ ChatID %d: no pude leer sus credenciales: %v", chatID, err)
		}
		return noCredentialsMessage
	}
	if userCredentials.CredsRejected {
		return noCredentialsMessage
	}
	items, _, err := notifier.fetchItemsFor(userCredentials)
	if errors.Is(err, ports.ErrBadCredentials) {
		notifier.markRejected(chatID)
		return noCredentialsMessage
	}
	if err != nil {
		log.Printf("⚠️ ChatID %d (/tps): %v", chatID, err)
		return pedcoUnavailableMessage
	}
	if len(items) == 0 {
		return nothingPendingMessage
	}
	return notifier.formatItems(items, false)
}

// LinkAccount guarda las credenciales de /login y las prueba contra PEDCO.
// Guarda antes de probar a propósito: si PEDCO está caído, los datos quedan.
func (notifier *Notifier) LinkAccount(chatID int64, username, password string) string {
	if err := notifier.userRepository.SaveUser(chatID, username, password); err != nil {
		log.Printf("⚠️ ChatID %d: error guardando credenciales: %v", chatID, err)
		return saveFailedMessage
	}
	token, err := notifier.source.Login(username, password)
	switch {
	case err == nil:
		if saveErr := notifier.userRepository.SaveSession(chatID, token); saveErr != nil {
			log.Printf("⚠️ ChatID %d: no se pudo persistir el token: %v", chatID, saveErr)
		}
		return loginOKMessage
	case errors.Is(err, ports.ErrBadCredentials):
		notifier.markRejected(chatID)
		return loginRejectedMessage
	default:
		log.Printf("⚠️ ChatID %d (/login): %v", chatID, err)
		return loginUnreachableMessage
	}
}

// fetchItemsFor usa el token guardado; si venció, reloguea UNA vez. Devuelve
// el token con el que terminó, que es el que usan las novedades.
func (notifier *Notifier) fetchItemsFor(userCredentials ports.UserCredentials) ([]domain.Item, string, error) {
	chatID := userCredentials.ChatID
	now := notifier.now()
	if userCredentials.Session != "" {
		items, err := notifier.source.FetchItems(userCredentials.Session, now)
		if err == nil {
			return items, userCredentials.Session, nil
		}
		if !errors.Is(err, ports.ErrSessionExpired) {
			return nil, "", fmt.Errorf("consulta con el token guardado falló: %w", err)
		}
		log.Printf("🔄 ChatID %d: token vencido, reloguear", chatID)
		_ = notifier.userRepository.ClearSession(chatID)
	}

	token, err := notifier.source.Login(userCredentials.User, userCredentials.Pass)
	if err != nil {
		return nil, "", fmt.Errorf("login falló: %w", err)
	}
	if saveErr := notifier.userRepository.SaveSession(chatID, token); saveErr != nil {
		log.Printf("⚠️ ChatID %d: no se pudo persistir el token: %v", chatID, saveErr)
	}
	items, err := notifier.source.FetchItems(token, now)
	if err != nil {
		return nil, "", fmt.Errorf("consulta tras el login falló: %w", err)
	}
	return items, token, nil
}

func (notifier *Notifier) markRejected(chatID int64) {
	log.Printf("🔑 ChatID %d: PEDCO rechazó las credenciales guardadas; en pausa", chatID)
	if err := notifier.userRepository.MarkCredentialsRejected(chatID); err != nil {
		log.Printf("⚠️ ChatID %d: no se pudo marcar el rechazo: %v", chatID, err)
	}
}

func hasPending(items []domain.Item) bool {
	for _, item := range items {
		if item.Status != domain.Done {
			return true
		}
	}
	return false
}
