package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

type MessageSender interface {
	Send(chatID int64, message string) error
}

type Notifier struct {
	userRepository ports.UserRepository
	makeScraper    ports.ScraperFactory
	messageSender  MessageSender
	delayBetween   time.Duration
	signatureBlock string
}

func NewNotifier(userRepository ports.UserRepository, makeScraper ports.ScraperFactory, messageSender MessageSender) *Notifier {
	return &Notifier{
		userRepository: userRepository,
		makeScraper:    makeScraper,
		messageSender:  messageSender,
		delayBetween:   3 * time.Second,
		signatureBlock: "\n---\n👨‍💻 *PedcoBot* | " +
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
		events, err := notifier.fetchEventsFor(
			userCredentials.ChatID,
			userCredentials.User,
			userCredentials.Pass,
			userCredentials.Session,
		)
		if err != nil {
			log.Printf("⚠️ ChatID %d: %v", userCredentials.ChatID, err)
			time.Sleep(notifier.delayBetween)
			continue
		}

		if len(events) > 0 {
			messageBody := notifier.formatEvents(events, true)
			if err := notifier.messageSender.Send(userCredentials.ChatID, messageBody); err != nil {
				log.Printf("⚠️ Falló envío ChatID %d: %v", userCredentials.ChatID, err)
			} else {
				log.Printf("✅ Notificación enviada a ChatID %d", userCredentials.ChatID)
			}
		}
		time.Sleep(notifier.delayBetween)
	}
	log.Println("🏁 Ronda finalizada.")
}

func (notifier *Notifier) NotifyOne(chatID int64) (string, error) {
	username, password, err := notifier.userRepository.GetUser(chatID)
	if err != nil || username == "" {
		return "", fmt.Errorf("sin credenciales: %w", err)
	}

	// /tps no tiene la sesión cargada en memoria; intenta refrescarla via login completo.
	// Si quisiéramos reusar sesión también acá, GetUser tendría que devolverla.
	events, err := notifier.fetchEventsFor(chatID, username, password, "")
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return notifier.formatEvents(events, false), nil
}

// fetchEventsFor implementa la estrategia cookie-first:
//  1. Si hay session blob, intentar usarlo directo (sin login).
//  2. Si Moodle devolvió ErrSessionExpired, limpiar caché y caer a login.
//  3. Login + scrape + guardar nueva sesión.
func (notifier *Notifier) fetchEventsFor(chatID int64, username, password, cachedSession string) ([]domain.Event, error) {
	if cachedSession != "" {
		cachedScraper := notifier.makeScraper()
		if err := cachedScraper.LoadSession(cachedSession); err == nil {
			events, fetchErr := cachedScraper.FetchEvents()
			if fetchErr == nil {
				log.Printf("♻️  ChatID %d: sesión cached válida (sin login)", chatID)
				return events, nil
			}
			if !errors.Is(fetchErr, ports.ErrSessionExpired) {
				return nil, fmt.Errorf("fetch con sesión cached falló: %w", fetchErr)
			}
			log.Printf("🔄 ChatID %d: sesión expirada, reloguear", chatID)
			_ = notifier.userRepository.ClearSession(chatID)
		}
	}

	freshScraper := notifier.makeScraper()
	if err := freshScraper.Login(username, password); err != nil {
		return nil, fmt.Errorf("login falló: %w", err)
	}

	// Guardar nueva sesión antes del scrape (si falla el save, no es fatal).
	if newSessionBlob, err := freshScraper.SessionBlob(); err == nil {
		if saveErr := notifier.userRepository.SaveSession(chatID, newSessionBlob); saveErr != nil {
			log.Printf("⚠️ ChatID %d: no se pudo persistir sesión: %v", chatID, saveErr)
		}
	}

	events, err := freshScraper.FetchEvents()
	if err != nil {
		return nil, fmt.Errorf("fetch eventos falló: %w", err)
	}
	return events, nil
}

func (notifier *Notifier) formatEvents(events []domain.Event, automaticRound bool) string {
	var messageBuilder strings.Builder
	if automaticRound {
		messageBuilder.WriteString("⏰ *Alerta Automática de Entregas:*\n\n")
	} else {
		messageBuilder.WriteString("📚 *Tus Próximos Eventos en Pedco:*\n\n")
	}
	for _, event := range events {
		fmt.Fprintf(&messageBuilder, "%s *%s*\n📘 Materia: %s\n⏰ Vence: %s\n🔗 [Ir a Pedco](%s)\n\n",
			event.Type, event.Title, event.Course, event.DueDate, event.Link)
	}
	messageBuilder.WriteString(notifier.signatureBlock)
	return messageBuilder.String()
}
