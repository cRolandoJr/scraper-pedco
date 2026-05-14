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
	mutex            sync.Mutex
	currentState     map[int64]string
	pendingUsername  map[int64]string
	stateStartedAt   map[int64]time.Time
}

const loginFlowTimeout = 5 * time.Minute

func newLoginFlow() *loginFlow {
	return &loginFlow{
		currentState:    make(map[int64]string),
		pendingUsername: make(map[int64]string),
		stateStartedAt:  make(map[int64]time.Time),
	}
}

func (flow *loginFlow) setState(chatID int64, newState string) {
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	flow.currentState[chatID] = newState
	flow.stateStartedAt[chatID] = time.Now()
}

func (flow *loginFlow) getState(chatID int64) string {
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	if startedAt, exists := flow.stateStartedAt[chatID]; exists && time.Since(startedAt) > loginFlowTimeout {
		delete(flow.currentState, chatID)
		delete(flow.pendingUsername, chatID)
		delete(flow.stateStartedAt, chatID)
		return ""
	}
	return flow.currentState[chatID]
}

func (flow *loginFlow) setPendingUsername(chatID int64, username string) {
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	flow.pendingUsername[chatID] = username
}

func (flow *loginFlow) consumePendingUsername(chatID int64) (username string, found bool) {
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	username, found = flow.pendingUsername[chatID]
	delete(flow.currentState, chatID)
	delete(flow.pendingUsername, chatID)
	delete(flow.stateStartedAt, chatID)
	return
}

// telegramSender implementa service.MessageSender.
type telegramSender struct{ bot *tele.Bot }

func (sender *telegramSender) Send(chatID int64, message string) error {
	_, err := sender.bot.Send(&tele.User{ID: chatID}, message, &tele.SendOptions{
		ParseMode:             tele.ModeMarkdown,
		DisableWebPagePreview: true,
	})
	return err
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontró archivo .env local")
	}

	telegramToken := os.Getenv("TG_TOKEN")
	if telegramToken == "" {
		log.Fatal("❌ Error: TG_TOKEN no está definido")
	}

	storage.InitDB()

	bot, err := tele.NewBot(tele.Settings{
		Token:  telegramToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatal("❌ Error al iniciar Telebot:", err)
	}

	// Wiring dependencias (composición sobre herencia).
	userRepository := storage.NewRepository()
	scraperFactory := ports.ScraperFactory(func() ports.Scraper { return pedco.NewPedcoScraper() })
	messageSender := &telegramSender{bot: bot}
	notifier := service.NewNotifier(userRepository, scraperFactory, messageSender)

	flow := newLoginFlow()

	argentinaLocation, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		log.Fatal("❌ Error cargando zona horaria:", err)
	}
	scheduler := cron.New(cron.WithLocation(argentinaLocation))
	if _, err := scheduler.AddFunc("0 8,20 * * *", notifier.NotifyAll); err != nil {
		log.Fatal("❌ Cron expression inválida:", err)
	}
	scheduler.Start()
	log.Println("⏱️  Cron activado (8:00 AM y 8:00 PM - Argentina)")

	registerHandlers(bot, notifier, flow)

	log.Println("🚀 Bot escuchando a Telegram...")
	bot.Start()
}

func registerHandlers(bot *tele.Bot, notifier *service.Notifier, flow *loginFlow) {
	bot.Handle("/start", func(context tele.Context) error {
		return context.Send(welcomeMessage(), markdownOpts())
	})

	bot.Handle("/login", func(context tele.Context) error {
		flow.setState(context.Sender().ID, "esperando_usuario")
		return context.Send("¡Perfecto! Vamos a vincular tu cuenta.\n\n👤 Escríbeme tu **Usuario o Legajo** de Pedco:", markdownOpts())
	})

	bot.Handle("/borrar", func(context tele.Context) error {
		if err := storage.DeleteUser(context.Sender().ID); err != nil {
			return context.Send("ℹ️ No tenías datos guardados.")
		}
		return context.Send("🗑️ Tus credenciales fueron eliminadas.")
	})

	bot.Handle("/tps", func(context tele.Context) error {
		message, err := notifier.NotifyOne(context.Sender().ID)
		if err != nil {
			return context.Send("❌ No tienes credenciales válidas. Usa /login.")
		}
		if message == "" {
			return context.Send("✅ ¡No tienes entregas pendientes! Relájate.")
		}
		return context.Send(message, markdownOpts())
	})

	bot.Handle(tele.OnText, func(context tele.Context) error {
		chatID := context.Sender().ID
		switch flow.getState(chatID) {
		case "esperando_usuario":
			flow.setPendingUsername(chatID, context.Text())
			flow.setState(chatID, "esperando_pass")
			return context.Send("✅ Usuario recibido.\n\n🔑 Ahora escríbeme tu <b>Contraseña</b> de Pedco:\n<i>(Se guarda cifrada con AES-256)</i>", &tele.SendOptions{ParseMode: tele.ModeHTML})

		case "esperando_pass":
			username, found := flow.consumePendingUsername(chatID)
			if !found {
				return context.Send("⚠️ Sesión expirada. Usa /login nuevamente.")
			}
			if err := storage.SaveUser(chatID, username, context.Text()); err != nil {
				log.Println("Error guardando en DB:", err)
				return context.Send("❌ Hubo un error guardando tus datos.")
			}
			return context.Send("🎉 ¡Cuenta vinculada! Usá /tps para ver tus entregas.")

		default:
			return context.Send(helpMessage(), &tele.SendOptions{ParseMode: tele.ModeHTML})
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
