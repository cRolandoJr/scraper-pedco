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
	users          ports.UserRepository
	makeScraper    ports.ScraperFactory
	sender         MessageSender
	delayBetween   time.Duration
	signatureBlock string
}

func NewNotifier(users ports.UserRepository, makeScraper ports.ScraperFactory, sender MessageSender) *Notifier {
	return &Notifier{
		users:        users,
		makeScraper:  makeScraper,
		sender:       sender,
		delayBetween: 3 * time.Second,
		signatureBlock: "\n---\n👨‍💻 *PedcoBot* | " +
			"[Rolando Cobis](https://linkedin.com/in/rolando-cobis-jr)",
	}
}

func (n *Notifier) NotifyAll() {
	log.Println("🤖 Iniciando ronda de revisión automática...")
	users, err := n.users.GetAllUsers()
	if err != nil {
		log.Println("❌ Error obteniendo usuarios:", err)
		return
	}

	for _, u := range users {
		events, err := n.fetchFor(u.ChatID, u.User, u.Pass, u.Session)
		if err != nil {
			log.Printf("⚠️ ChatID %d: %v", u.ChatID, err)
			time.Sleep(n.delayBetween)
			continue
		}

		if len(events) > 0 {
			msg := n.formatEvents(events, true)
			if err := n.sender.Send(u.ChatID, msg); err != nil {
				log.Printf("⚠️ Falló envío ChatID %d: %v", u.ChatID, err)
			} else {
				log.Printf("✅ Notificación enviada a ChatID %d", u.ChatID)
			}
		}
		time.Sleep(n.delayBetween)
	}
	log.Println("🏁 Ronda finalizada.")
}

func (n *Notifier) NotifyOne(chatID int64) (string, error) {
	user, pass, err := n.users.GetUser(chatID)
	if err != nil || user == "" {
		return "", fmt.Errorf("sin credenciales: %w", err)
	}

	// /tps no tiene la sesión cargada en memoria; intenta refrescarla via login completo.
	// Si quisiéramos reusar sesión también acá, GetUser tendría que devolverla.
	// Por simplicidad la pedimos junto al fetchFor flow estándar (cacheable abajo).
	events, err := n.fetchFor(chatID, user, pass, "")
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return n.formatEvents(events, false), nil
}

// fetchFor implementa la estrategia cookie-first:
//  1. Si hay session blob, intentar usarlo directo (sin login).
//  2. Si Moodle devolvió ErrSessionExpired, limpiar caché y caer a login.
//  3. Login + scrape + guardar nueva sesión.
func (n *Notifier) fetchFor(chatID int64, user, pass, session string) ([]domain.Event, error) {
	if session != "" {
		scraper := n.makeScraper()
		if err := scraper.LoadSession(session); err == nil {
			events, err := scraper.FetchEvents()
			if err == nil {
				log.Printf("♻️  ChatID %d: sesión cached válida (sin login)", chatID)
				return events, nil
			}
			if !errors.Is(err, ports.ErrSessionExpired) {
				return nil, fmt.Errorf("fetch con sesión cached falló: %w", err)
			}
			log.Printf("🔄 ChatID %d: sesión expirada, reloguear", chatID)
			_ = n.users.ClearSession(chatID)
		}
	}

	scraper := n.makeScraper()
	if err := scraper.Login(user, pass); err != nil {
		return nil, fmt.Errorf("login falló: %w", err)
	}

	// Guardar nueva sesión antes del scrape (si falla el save, no es fatal).
	if blob, err := scraper.SessionBlob(); err == nil {
		if saveErr := n.users.SaveSession(chatID, blob); saveErr != nil {
			log.Printf("⚠️ ChatID %d: no se pudo persistir sesión: %v", chatID, saveErr)
		}
	}

	events, err := scraper.FetchEvents()
	if err != nil {
		return nil, fmt.Errorf("fetch eventos falló: %w", err)
	}
	return events, nil
}

func (n *Notifier) formatEvents(events []domain.Event, automatic bool) string {
	var b strings.Builder
	if automatic {
		b.WriteString("⏰ *Alerta Automática de Entregas:*\n\n")
	} else {
		b.WriteString("📚 *Tus Próximos Eventos en Pedco:*\n\n")
	}
	for _, ev := range events {
		fmt.Fprintf(&b, "%s *%s*\n📘 Materia: %s\n⏰ Vence: %s\n🔗 [Ir a Pedco](%s)\n\n",
			ev.Type, ev.Title, ev.Course, ev.DueDate, ev.Link)
	}
	b.WriteString(n.signatureBlock)
	return b.String()
}
