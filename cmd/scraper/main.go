package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/joho/godotenv"
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

// Healthcheck: cada healthcheckInterval llamamos getMe contra la API de
// Telegram para verificar que el long-poll no quedó zombi. Tras
// healthcheckMaxFailures consecutivos, os.Exit(1) y systemd reinicia.
const (
	healthcheckInterval    = 2 * time.Minute
	healthcheckMaxFailures = 3
)

// tokenPattern matchea el token de Telegram embebido en URLs (errores de la
// lib telebot exponen la URL completa con el token); lo enmascaramos antes
// de loguear para no leakearlo en journalctl.
var tokenPattern = regexp.MustCompile(`bot\d+:[A-Za-z0-9_-]+`)

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	return tokenPattern.ReplaceAllString(err.Error(), "bot***:***")
}

// buildHTTPClient retorna un http.Client con TCP keepalive corto y timeouts
// explícitos. Sin esto, telebot usa http.DefaultClient (sin protecciones)
// y el long-poll queda esperando indefinidamente cuando la conexión TCP
// muere silenciosamente (suspend/resume, cambio de wifi, NAT rebind).
// Con KeepAlive=30s, Go detecta el socket muerto en ~30s y retry-ea.
func buildHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 35 * time.Second, // long-poll Telegram = 10s; margen 25s.
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// startHealthcheck arranca una goroutine que invoca getMe periódicamente.
// Si falla healthcheckMaxFailures veces seguidas, log.Fatalf termina el
// proceso y systemd lo levanta (Restart=always en el unit file).
func startHealthcheck(bot *tele.Bot) {
	go func() {
		ticker := time.NewTicker(healthcheckInterval)
		defer ticker.Stop()
		consecutiveFailures := 0
		for range ticker.C {
			if _, err := bot.Raw("getMe", nil); err != nil {
				consecutiveFailures++
				log.Printf("⚠️  Healthcheck falló (%d/%d): %s", consecutiveFailures, healthcheckMaxFailures, sanitize(err))
				if consecutiveFailures >= healthcheckMaxFailures {
					log.Fatalf("❌ %d healthchecks consecutivos fallidos — reiniciando service.", healthcheckMaxFailures)
				}
				continue
			}
			if consecutiveFailures > 0 {
				log.Printf("✅ Healthcheck recuperado tras %d fallos.", consecutiveFailures)
			}
			consecutiveFailures = 0
		}
	}()
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
		Client: buildHTTPClient(),
	})
	if err != nil {
		log.Fatalf("❌ Error al iniciar Telebot: %s", sanitize(err))
	}

	// Wiring dependencias (composición sobre herencia).
	userRepository := storage.NewRepository()
	scraperFactory := ports.ScraperFactory(func() ports.Scraper { return pedco.NewPedcoScraper() })
	messageSender := &telegramSender{bot: bot}
	notifier := service.NewNotifier(userRepository, scraperFactory, messageSender)

	// Modo oneshot: lo dispara el systemd timer (Persistent=true). Manda una
	// ronda de avisos y sale; no abre long-poll ni handlers.
	if len(os.Args) > 1 && os.Args[1] == "notify" {
		log.Println("📨 Modo notify: ronda única y salida.")
		notifier.NotifyAll()
		return
	}

	// Daemon: handlers + healthcheck + long-poll. El scheduling de los avisos
	// 8/20h lo maneja un systemd timer externo (pedco-bot notify), no un cron
	// interno — así el catch-up tras suspend/apagado lo da systemd.
	flow := newLoginFlow()
	registerHandlers(bot, notifier, flow)
	startHealthcheck(bot)

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
		"👉 /login - vincular tu cuenta.\n" +
		"👉 /tps - revisar entregas pendientes.\n\n" +
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
