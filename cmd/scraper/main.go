package main

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	tele "gopkg.in/telebot.v3"

	"scraper-pedco/internal/adapters/pedco"
	"scraper-pedco/internal/adapters/storage"
	"scraper-pedco/internal/core/ports"
	"scraper-pedco/internal/core/service"
)

// loginFlow protege el estado conversacional contra accesos concurrentes
// (Telebot dispara handlers en goroutines).
type loginFlow struct {
	mu        sync.Mutex
	state     map[int64]string
	tempUser  map[int64]string
	createdAt map[int64]time.Time
}

const loginFlowTTL = 5 * time.Minute

func newLoginFlow() *loginFlow {
	return &loginFlow{
		state:     make(map[int64]string),
		tempUser:  make(map[int64]string),
		createdAt: make(map[int64]time.Time),
	}
}

func (f *loginFlow) setState(chatID int64, s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[chatID] = s
	f.createdAt[chatID] = time.Now()
}

func (f *loginFlow) getState(chatID int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.createdAt[chatID]; ok && time.Since(t) > loginFlowTTL {
		delete(f.state, chatID)
		delete(f.tempUser, chatID)
		delete(f.createdAt, chatID)
		return ""
	}
	return f.state[chatID]
}

func (f *loginFlow) setTempUser(chatID int64, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tempUser[chatID] = user
}

func (f *loginFlow) consume(chatID int64) (user string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok = f.tempUser[chatID]
	delete(f.state, chatID)
	delete(f.tempUser, chatID)
	delete(f.createdAt, chatID)
	return
}

// telegramSender implementa service.MessageSender.
type telegramSender struct{ bot *tele.Bot }

func (t *telegramSender) Send(chatID int64, msg string) error {
	_, err := t.bot.Send(&tele.User{ID: chatID}, msg, &tele.SendOptions{
		ParseMode:             tele.ModeMarkdown,
		DisableWebPagePreview: true,
	})
	return err
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontró archivo .env local")
	}

	token := os.Getenv("TG_TOKEN")
	if token == "" {
		log.Fatal("❌ Error: TG_TOKEN no está definido")
	}

	storage.InitDB()

	b, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal("❌ Error al iniciar Telebot:", err)
	}

	// Wiring dependencias (composición sobre herencia).
	repo := storage.NewRepository()
	scraperFactory := ports.ScraperFactory(func() ports.Scraper { return pedco.NewPedcoScraper() })
	sender := &telegramSender{bot: b}
	notifier := service.NewNotifier(repo, scraperFactory, sender)

	flow := newLoginFlow()

	// Cron: Argentina, 8:00 y 20:00.
	argLocation, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		log.Fatal("❌ Error cargando zona horaria:", err)
	}
	c := cron.New(cron.WithLocation(argLocation))
	if _, err := c.AddFunc("0 8,20 * * *", notifier.NotifyAll); err != nil {
		log.Fatal("❌ Cron expression inválida:", err)
	}
	c.Start()
	log.Println("⏱️  Cron activado (8:00 AM y 8:00 PM - Argentina)")

	registerHandlers(b, notifier, flow)

	log.Println("🚀 Bot escuchando a Telegram...")
	b.Start()
}

func registerHandlers(b *tele.Bot, notifier *service.Notifier, flow *loginFlow) {
	b.Handle("/start", func(c tele.Context) error {
		return c.Send(welcomeMessage(), markdownOpts())
	})

	b.Handle("/login", func(c tele.Context) error {
		flow.setState(c.Sender().ID, "esperando_usuario")
		return c.Send("¡Perfecto! Vamos a vincular tu cuenta.\n\n👤 Escríbeme tu **Usuario o Legajo** de Pedco:", markdownOpts())
	})

	b.Handle("/borrar", func(c tele.Context) error {
		if err := storage.DeleteUser(c.Sender().ID); err != nil {
			return c.Send("ℹ️ No tenías datos guardados.")
		}
		return c.Send("🗑️ Tus credenciales fueron eliminadas.")
	})

	b.Handle("/tps", func(c tele.Context) error {
		msg, err := notifier.NotifyOne(c.Sender().ID)
		if err != nil {
			return c.Send("❌ No tienes credenciales válidas. Usa /login.")
		}
		if msg == "" {
			return c.Send("✅ ¡No tienes entregas pendientes! Relájate.")
		}
		return c.Send(msg, markdownOpts())
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		chatID := c.Sender().ID
		switch flow.getState(chatID) {
		case "esperando_usuario":
			flow.setTempUser(chatID, c.Text())
			flow.setState(chatID, "esperando_pass")
			return c.Send("✅ Usuario recibido.\n\n🔑 Ahora escríbeme tu <b>Contraseña</b> de Pedco:\n<i>(Se guarda cifrada con AES-256)</i>", &tele.SendOptions{ParseMode: tele.ModeHTML})

		case "esperando_pass":
			usuario, ok := flow.consume(chatID)
			if !ok {
				return c.Send("⚠️ Sesión expirada. Usa /login nuevamente.")
			}
			if err := storage.SaveUser(chatID, usuario, c.Text()); err != nil {
				log.Println("Error guardando en DB:", err)
				return c.Send("❌ Hubo un error guardando tus datos.")
			}
			return c.Send("🎉 ¡Cuenta vinculada! Usá /tps para ver tus entregas.")

		default:
			return c.Send(helpMessage(), &tele.SendOptions{ParseMode: tele.ModeHTML})
		}
	})
}

func markdownOpts() *tele.SendOptions {
	return &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true}
}

func welcomeMessage() string {
	return "🎓 *¡Hola! Soy el Bot de Pedco UNComa.*\n\n" +
		"Puedo revisar la plataforma por ti y avisarte de tus entregas.\n\n" +
		"👉 Usa /login para vincular tu cuenta.\n" +
		"👉 Usa /tps para revisar tus entregas pendientes.\n\n" +
		"---\n" +
		"👨‍💻 *Desarrollado por [Rolando Cobis](https://linkedin.com/in/rolando-cobis-jr)*"
}

func helpMessage() string {
	return "🤔 <b>No reconocí ese comando o mensaje.</b>\n\n" +
		"Comandos disponibles:\n\n" +
		"👉 /login - Vincular o actualizar tu cuenta de Pedco.\n" +
		"👉 /tps - Revisar tus entregas pendientes ahora.\n" +
		"👉 /borrar - Eliminar tus datos del sistema.\n" +
		"👉 /start - Ver el mensaje de bienvenida.\n\n" +
		"💡 Revisión automática diaria: 8 AM y 8 PM (Argentina)."
}
