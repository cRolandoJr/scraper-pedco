package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	tele "gopkg.in/telebot.v3"

	// IMPORTAMOS NUESTROS ADAPTADORES
	"scraper-pedco/internal/adapters/pedco"
	"scraper-pedco/internal/adapters/storage"
)

var userState = make(map[int64]string)
var tempUser = make(map[int64]string)

func main() {
	storage.InitDB()

	err := godotenv.Load()
	if err != nil {
		log.Println("Aviso: No se encontró archivo .env local, usando variables del sistema")
	}

	token := os.Getenv("TG_TOKEN")
	if token == "" {
		log.Fatal("❌ Error: TG_TOKEN no está definido")
	}

	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal("❌ Error al iniciar Telebot:", err)
		return
	}

	// ---------------------------------------------------------
	// COMANDOS DEL BOT
	// ---------------------------------------------------------

	b.Handle("/start", func(c tele.Context) error {
		mensaje := "🎓 *¡Hola! Soy el Bot de Pedco UNComa.*\n\n" +
			"Puedo revisar la plataforma por ti y avisarte de tus entregas.\n\n" +
			"👉 Usa /login para vincular tu cuenta.\n" +
			"👉 Usa /tps para revisar tus entregas pendientes."
		return c.Send(mensaje, &tele.SendOptions{ParseMode: tele.ModeMarkdown})
	})

	b.Handle("/login", func(c tele.Context) error {
		chatID := c.Sender().ID
		userState[chatID] = "esperando_usuario"
		return c.Send("¡Perfecto! Vamos a vincular tu cuenta.\n\n👤 Por favor, escríbeme tu **Usuario o Legajo** de Pedco:", &tele.SendOptions{ParseMode: tele.ModeMarkdown})
	})

	// NUEVO COMANDO: Borrar credenciales (Ética y Seguridad)
	b.Handle("/borrar", func(c tele.Context) error {
		chatID := c.Sender().ID
		// Aquí podríamos hacer un storage.DeleteUser(chatID), pero por ahora lo sobrescribimos con texto vacío
		storage.SaveUser(chatID, "", "")
		return c.Send("🗑️ Tus credenciales han sido eliminadas de mi base de datos.")
	})

	// NUEVO COMANDO: Hacer el Scraping a demanda
	b.Handle("/tps", func(c tele.Context) error {
		chatID := c.Sender().ID

		// 1. Buscamos al usuario en la BD
		usuario, password, err := storage.GetUser(chatID)
		if err != nil || usuario == "" {
			return c.Send("❌ No encontré tus credenciales. Por favor, usa /login primero.")
		}

		// 2. Avisamos que el proceso inició (el scraping demora unos segundos)
		c.Send("⏳ Conectando con Pedco... Revisando el calendario...")

		// 3. Iniciamos el scraper con las credenciales de ESTA persona
		scraper := pedco.NewPedcoScraper()
		err = scraper.Login(usuario, password)
		if err != nil {
			log.Printf("Error de login para ChatID %d: %v", chatID, err)
			return c.Send("❌ Falló el inicio de sesión. ¿Tus credenciales son correctas? Usa /login para actualizarlas.")
		}

		events, err := scraper.FetchEvents()
		if err != nil {
			log.Printf("Error extrayendo eventos para ChatID %d: %v", chatID, err)
			return c.Send("❌ Hubo un error al leer la plataforma. Pedco podría estar caído.")
		}

		// 4. Si no hay eventos, lo felicitamos
		if len(events) == 0 {
			return c.Send("✅ ¡Estás al día! No tienes trabajos prácticos ni parciales próximos.")
		}

		// 5. Si hay eventos, armamos el mensaje y lo enviamos
		mensaje := "📚 *Tus Próximos Eventos en Pedco:*\n\n"
		for _, ev := range events {
			mensaje += fmt.Sprintf("%s *%s*\n📘 Materia: %s\n⏰ Vence: %s\n🔗 [Ir a Pedco](%s)\n\n",
				ev.Type, ev.Title, ev.Course, ev.DueDate, ev.Link)
		}

		// DisableWebPagePreview evita que Telegram genere miniaturas gigantes por cada link
		return c.Send(mensaje, &tele.SendOptions{ParseMode: tele.ModeMarkdown, DisableWebPagePreview: true})
	})

	// ---------------------------------------------------------
	// MÁQUINA DE ESTADOS (Manejador de Texto)
	// ---------------------------------------------------------

	b.Handle(tele.OnText, func(c tele.Context) error {
		chatID := c.Sender().ID
		textoRecibido := c.Text()
		estadoActual := userState[chatID]

		switch estadoActual {
		case "esperando_usuario":
			tempUser[chatID] = textoRecibido
			userState[chatID] = "esperando_pass"
			return c.Send("✅ Registrado. Ahora, escríbeme tu **Contraseña** de Pedco:", &tele.SendOptions{ParseMode: tele.ModeMarkdown})

		case "esperando_pass":
			usuario := tempUser[chatID]
			password := textoRecibido

			err := storage.SaveUser(chatID, usuario, password)
			if err != nil {
				log.Println("Error guardando en DB:", err)
				return c.Send("❌ Hubo un error guardando tus datos.")
			}

			delete(userState, chatID)
			delete(tempUser, chatID)

			mensajeExito := fmt.Sprintf("🎉 ¡Cuenta vinculada!\nUsuario: %s\n\n👉 Ya puedes usar el comando /tps para ver tus entregas.", usuario)
			return c.Send(mensajeExito)

		default:
			return c.Send("No entendí ese comando. Usa /start para ver el menú.")
		}
	})

	log.Println("🚀 Servidor interactivo iniciado. Escuchando a Telegram...")
	b.Start()
}
