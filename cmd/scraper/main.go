package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	tele "gopkg.in/telebot.v3"

	"scraper-pedco/internal/adapters/moodle"
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

// TopicRepository guarda el tema de cada (chat, canal). Lo usa solo telegramSender.
type TopicRepository interface {
	TopicID(chatID int64, channel ports.Channel) (threadID int, found bool, err error)
	SaveTopic(chatID int64, channel ports.Channel, threadID int) error // INSERT OR IGNORE; después se relee
	ForgetTopic(chatID int64, channel ports.Channel) error
}

type topicKey struct {
	chatID  int64
	channel ports.Channel
}

// telegramSender implementa service.MessageSender. failedTopics recuerda, mientras
// dura el proceso, los (chat, canal) cuyo tema no se pudo crear.
type telegramSender struct {
	bot          *tele.Bot
	topics       TopicRepository
	failedTopics map[topicKey]bool
}

func newTelegramSender(bot *tele.Bot, topics TopicRepository) *telegramSender {
	return &telegramSender{bot: bot, topics: topics, failedTopics: make(map[topicKey]bool)}
}

var topicNames = map[ports.Channel]string{
	ports.ChannelDeliveries: "📚 Entregas",
	ports.ChannelNews:       "📣 Novedades",
}

func (sender *telegramSender) Send(chatID int64, channel ports.Channel, message string) error {
	return sender.deliver(chatID, channel, message, tele.SendOptions{
		ParseMode:             tele.ModeMarkdown,
		DisableWebPagePreview: true,
	})
}

func (sender *telegramSender) SendPlain(chatID int64, channel ports.Channel, message string) error {
	return sender.deliver(chatID, channel, message, tele.SendOptions{DisableWebPagePreview: true})
}

// deliver: un tema borrado se recrea UNA vez; si tampoco sirve, el mensaje sale fuera de tema.
func (sender *telegramSender) deliver(chatID int64, channel ports.Channel, message string, options tele.SendOptions) error {
	if channel == ports.ChannelGeneral {
		return classifySendError(sender.sendTo(chatID, 0, message, options))
	}
	threadID := sender.threadFor(chatID, channel)
	err := sender.sendTo(chatID, threadID, message, options)
	if threadID == 0 || !isThreadNotFound(err) {
		return classifySendError(err)
	}
	log.Printf("🧵 ChatID %d: el tema %q ya no existe; se recrea", chatID, channel)
	if forgetErr := sender.topics.ForgetTopic(chatID, channel); forgetErr != nil {
		log.Printf("⚠️ ChatID %d: no pude olvidar el tema %q: %v", chatID, channel, forgetErr)
	}
	threadID = sender.threadFor(chatID, channel)
	err = sender.sendTo(chatID, threadID, message, options)
	if threadID != 0 && isThreadNotFound(err) {
		err = sender.sendTo(chatID, 0, message, options)
	}
	return classifySendError(err)
}

func (sender *telegramSender) sendTo(chatID int64, threadID int, message string, options tele.SendOptions) error {
	options.ThreadID = threadID
	_, err := sender.bot.Send(&tele.User{ID: chatID}, message, &options)
	return err
}

// threadFor devuelve el tema del canal, creándolo si no hay; 0 = fuera de tema.
func (sender *telegramSender) threadFor(chatID int64, channel ports.Channel) int {
	key := topicKey{chatID: chatID, channel: channel}
	if sender.failedTopics[key] {
		return 0
	}
	threadID, found, err := sender.topics.TopicID(chatID, channel)
	if err != nil {
		log.Printf("⚠️ ChatID %d: no pude leer el tema %q; va fuera de tema: %v", chatID, channel, err)
		return 0
	}
	if found {
		return threadID
	}
	topic, err := sender.bot.CreateTopic(&tele.Chat{ID: chatID}, &tele.Topic{Name: topicNames[channel]})
	if err != nil || topic == nil || topic.ThreadID == 0 {
		sender.failedTopics[key] = true
		reason := "Telegram no devolvió el tema"
		if err != nil {
			reason = sanitize(err)
		}
		log.Printf("⚠️ ChatID %d: no pude crear el tema %q; va fuera de tema: %s", chatID, channel, reason)
		return 0
	}
	if err := sender.topics.SaveTopic(chatID, channel, topic.ThreadID); err != nil {
		sender.failedTopics[key] = true
		log.Printf("⚠️ ChatID %d: no pude guardar el tema %q; se usa sin guardar: %v", chatID, channel, err)
		return topic.ThreadID
	}
	if savedID, found, err := sender.topics.TopicID(chatID, channel); err == nil && found {
		return savedID
	}
	return topic.ThreadID
}

// isThreadNotFound: telebot no tipa este error; llega como texto.
func isThreadNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message thread not found")
}

// telegramErrorCode: telebot arma las descripciones que no tiene mapeadas (el
// Markdown rechazado es una) con fmt.Errorf("telegram: %s (%d)"), sin tipo.
var telegramErrorCode = regexp.MustCompile(`^telegram: .* \((\d{3})\)$`)

// classifySendError: 401 es ports.ErrSendUnauthorized; los demás 4xx salvo 429,
// permanentes (ports.ErrSendPermanent); red, 429 y 5xx, transitorio. El error sale sin el token del bot.
func classifySendError(err error) error {
	if err == nil {
		return nil
	}
	code := 0
	var floodErr tele.FloodError
	var apiErr *tele.Error
	switch {
	case errors.As(err, &floodErr):
		code = http.StatusTooManyRequests
	case errors.As(err, &apiErr):
		code = apiErr.Code
	default:
		if match := telegramErrorCode.FindStringSubmatch(err.Error()); match != nil {
			code, _ = strconv.Atoi(match[1])
		}
	}
	if code == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ports.ErrSendUnauthorized, sanitize(err))
	}
	if code >= 400 && code < 500 && code != http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s", ports.ErrSendPermanent, sanitize(err))
	}
	return errors.New(sanitize(err))
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

	argentinaLocation, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		log.Fatal("❌ Error cargando la zona horaria: ", err)
	}

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
	source := moodle.NewClient(moodle.DefaultBaseURL, argentinaLocation)
	messageSender := newTelegramSender(bot, userRepository)
	notifier := service.NewNotifier(userRepository, userRepository, source, messageSender, argentinaLocation)

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
		return context.Send(welcomeMessage(), inTopic(context, markdownOpts()))
	})

	bot.Handle("/login", func(context tele.Context) error {
		flow.setState(context.Sender().ID, "esperando_usuario")
		return context.Send("¡Perfecto! Vamos a vincular tu cuenta.\n\n👤 Escríbeme tu **Usuario o Legajo** de Pedco:", inTopic(context, markdownOpts()))
	})

	bot.Handle("/borrar", func(context tele.Context) error {
		if err := storage.DeleteUser(context.Sender().ID); err != nil {
			return context.Send("ℹ️ No tenías datos guardados.", inTopic(context, &tele.SendOptions{}))
		}
		return context.Send("🗑️ Tus credenciales fueron eliminadas.", inTopic(context, &tele.SendOptions{}))
	})

	bot.Handle("/tps", func(context tele.Context) error {
		return context.Send(notifier.NotifyOne(context.Sender().ID), inTopic(context, markdownOpts()))
	})

	bot.Handle(tele.OnText, func(context tele.Context) error {
		chatID := context.Sender().ID
		switch flow.getState(chatID) {
		case "esperando_usuario":
			flow.setPendingUsername(chatID, context.Text())
			flow.setState(chatID, "esperando_pass")
			return context.Send("✅ Usuario recibido.\n\n🔑 Ahora escríbeme tu <b>Contraseña</b> de Pedco:\n<i>(Se guarda cifrada con AES-256)</i>", inTopic(context, &tele.SendOptions{ParseMode: tele.ModeHTML}))

		case "esperando_pass":
			username, found := flow.consumePendingUsername(chatID)
			if !found {
				return context.Send("⚠️ Sesión expirada. Usa /login nuevamente.", inTopic(context, &tele.SendOptions{}))
			}
			return context.Send(notifier.LinkAccount(chatID, username, context.Text()), inTopic(context, &tele.SendOptions{}))

		default:
			return context.Send(helpMessage(), inTopic(context, &tele.SendOptions{ParseMode: tele.ModeHTML}))
		}
	})
}

// inTopic: la respuesta va al tema donde escribió el usuario (0 = fuera de tema).
func inTopic(context tele.Context, options *tele.SendOptions) *tele.SendOptions {
	options.ThreadID = context.Message().ThreadID
	return options
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
