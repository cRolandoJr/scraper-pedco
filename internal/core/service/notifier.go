package service

import (
	"fmt"
	"log"
	"strings"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

// MessageSender abstrae el envío de mensajes (Telegram, WhatsApp, etc).
type MessageSender interface {
	Send(chatID int64, message string) error
}

// Notifier orquesta scraping + notificación.
// No conoce SQLite, Telebot ni Colly directamente.
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

// NotifyAll recorre todos los usuarios y manda alerta si hay eventos.
func (n *Notifier) NotifyAll() {
	log.Println("🤖 Iniciando ronda de revisión automática...")
	users, err := n.users.GetAllUsers()
	if err != nil {
		log.Println("❌ Error obteniendo usuarios:", err)
		return
	}

	for _, u := range users {
		events, err := n.fetchFor(u.User, u.Pass)
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

// NotifyOne ejecuta scraping bajo demanda (comando /tps).
// Retorna el mensaje listo para enviar, o cadena vacía si no hay eventos.
func (n *Notifier) NotifyOne(chatID int64) (string, error) {
	user, pass, err := n.users.GetUser(chatID)
	if err != nil || user == "" {
		return "", fmt.Errorf("sin credenciales: %w", err)
	}

	events, err := n.fetchFor(user, pass)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return n.formatEvents(events, false), nil
}

func (n *Notifier) fetchFor(user, pass string) ([]domain.Event, error) {
	scraper := n.makeScraper()
	if err := scraper.Login(user, pass); err != nil {
		return nil, fmt.Errorf("login falló: %w", err)
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
