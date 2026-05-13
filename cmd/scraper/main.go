package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	// Importamos nuestros adaptadores (cambia "tu-usuario" por el tuyo)
	"scraper-pedco/internal/adapters/pedco"
	"scraper-pedco/internal/adapters/telegram"

	"github.com/joho/godotenv"
)

func asistenteConfiguracion() {
	// Verificamos si el archivo .env ya existe
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		fmt.Println(" ¡Bienvenido a PedcoBot UNComa!")
		fmt.Println("Es tu primera vez abriendo el programa. Vamos a configurarlo (solo lo harás una vez).")

		reader := bufio.NewReader(os.Stdin)

		// 1. Pedimos Credenciales de Pedco
		fmt.Print("\n1️.  Ingresa tu usuario de Pedco: ")
		user, _ := reader.ReadString('\n')
		user = strings.TrimSpace(user)

		fmt.Print("2️. Ingresa tu contraseña de Pedco: ")
		pass, _ := reader.ReadString('\n')
		pass = strings.TrimSpace(pass)

		// 2. Pedimos el Chat ID y explicamos cómo obtenerlo
		fmt.Println("\nPara enviarte los avisos, necesito tu ID de Telegram.")
		fmt.Println("Paso A: Abre Telegram y envíale un 'Hola' a mi bot oficial: @scraperPedcobot")
		fmt.Println("Paso B: Luego, busca el bot @userinfobot, envíale un mensaje y copia el número que te da.")
		fmt.Print("3️⃣  Pega ese número (tu Chat ID) aquí: ")
		chatID, _ := reader.ReadString('\n')
		chatID = strings.TrimSpace(chatID)

		tokenFijo := "ID-bot"

		envContent := fmt.Sprintf("PEDCO_USER=%s\nPEDCO_PASS=%s\nTG_TOKEN=%s\nTG_CHAT_ID=%s\n", user, pass, tokenFijo, chatID)

		err = os.WriteFile(".env", []byte(envContent), 0644)
		if err != nil {
			log.Fatal("Error creando el archivo de configuración:", err)
		}

		fmt.Println("\n✅ ¡Listo! Credenciales guardadas con éxito de forma local.")
		fmt.Println("Iniciando la búsqueda de trabajos prácticos...")
		fmt.Println("\n--------------------------------------------------")
	}
}

func main() {
	log.Println("🚀 Iniciando el Worker de Pedco...")

	asistenteConfiguracion()

	err := godotenv.Load()
	if err != nil {
		log.Println("ℹ️ No se encontró archivo .env local, usando variables del sistema.")
	}

	pedcoUser := os.Getenv("PEDCO_USER")
	pedcoPass := os.Getenv("PEDCO_PASS")
	tgToken := os.Getenv("TG_TOKEN")
	tgChatID := os.Getenv("TG_CHAT_ID")

	if pedcoUser == "" || tgToken == "" {
		log.Fatal("Faltan variables de entorno. Abortando.")
	}

	scraper := pedco.NewPedcoScraper()
	notifier := telegram.NewTelegramNotifier(tgToken, tgChatID)

	log.Println("Intentando iniciar sesión en la plataforma...")
	err = scraper.Login(pedcoUser, pedcoPass)
	if err != nil {
		log.Fatalf("Error en login: %v", err)
	}

	log.Println("Buscando eventos y trabajos prácticos...")
	events, err := scraper.FetchEvents()
	if err != nil {
		log.Fatalf("Error extrayendo eventos: %v", err)
	}

	if len(events) == 0 {
		log.Println("No hay eventos próximos. Terminando ejecución en silencio.")
		return
	}

	mensaje := "Resumen de Pedco UNComa:\n\n"
	for _, ev := range events {
		mensaje += fmt.Sprintf("📝 %s\n📘 Materia: %s\n⏰ Vence: %s\n🔗 [Link de entrega](%s)\n\n",
			ev.Title, ev.Course, ev.DueDate, ev.Link)
	}

	mensaje += "---\n"
	mensaje += "PedcoBot | [Desarrollado por Rolando Cobis](https://linkedin.com/in/rolando-cobis-jr)"

	log.Println("Enviando mensaje push al teléfono...")
	err = notifier.SendPush(mensaje)
	if err != nil {
		log.Fatalf("Error enviando Telegram: %v", err)
	}

	log.Println("✅ Ejecución finalizada con éxito.")
}
