package main

import (
	"fmt"
	"log"
	"os"

	// Importamos nuestros adaptadores (cambia "tu-usuario" por el tuyo)
	"scraper-pedco/internal/adapters/pedco"
	"scraper-pedco/internal/adapters/telegram"

	"github.com/joho/godotenv"
)

func main() {
	log.Println("🚀 Iniciando el Worker de Pedco...")

	// 0. CARGAR EL ARCHIVO .env
	// Si el archivo existe, carga las variables. Si no existe (ej. en producción con systemd), sigue de largo sin fallar.
	err := godotenv.Load()
	if err != nil {
		log.Println("ℹ️ No se encontró archivo .env local, usando variables del sistema.")
	}

	// 1. LEER SECRETOS
	pedcoUser := os.Getenv("PEDCO_USER")
	pedcoPass := os.Getenv("PEDCO_PASS")
	tgToken := os.Getenv("TG_TOKEN")
	tgChatID := os.Getenv("TG_CHAT_ID")

	if pedcoUser == "" || pedcoPass == "" || tgToken == "" || tgChatID == "" {
		log.Fatal("❌ Faltan variables de entorno. Revisa tus credenciales. Abortando.")
	}

	// 2. INSTANCIAR ADAPTADORES (Inyección de dependencias)
	scraper := pedco.NewPedcoScraper()
	notifier := telegram.NewTelegramNotifier(tgToken, tgChatID)

	// 3. EJECUTAR LÓGICA DE NEGOCIO
	log.Println("Intentando iniciar sesión en la plataforma...")
	err = scraper.Login(pedcoUser, pedcoPass)
	if err != nil {
		log.Fatalf("❌ Error en login: %v", err)
	}

	log.Println("Buscando eventos y trabajos prácticos...")
	events, err := scraper.FetchEvents()
	if err != nil {
		log.Fatalf("❌ Error extrayendo eventos: %v", err)
	}

	// 4. PREPARAR EL MENSAJE
	if len(events) == 0 {
		log.Println("No hay eventos próximos. Terminando ejecución en silencio.")
		return
	}

	// Formateamos el texto usando Markdown básico de Telegram
	mensaje := "📚 *Resumen de Pedco UNComa:*\n\n"
	for _, ev := range events {
		mensaje += fmt.Sprintf("📝 *%s*\n📘 Materia: %s\n⏰ Vence: %s\n🔗 [Link de entrega](%s)\n\n",
			ev.Title, ev.Course, ev.DueDate, ev.Link)
	}

	// Agregamos un separador sutil y la firma como un enlace integrado
	mensaje += "---\n"
	mensaje += "🤖 *PedcoBot* | [Desarrollado por Rolando Cobis](https://linkedin.com/in/rolando-cobis-jr)"

	// 5. ENVIAR NOTIFICACIÓN
	log.Println("Enviando mensaje push al teléfono...")
	err = notifier.SendPush(mensaje)
	if err != nil {
		log.Fatalf("❌ Error enviando Telegram: %v", err)
	}

	log.Println("✅ Ejecución finalizada con éxito.")
}
