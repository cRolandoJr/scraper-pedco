package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// TelegramNotifier es el adaptador que se comunica con la API oficial de Telegram
type TelegramNotifier struct {
	botToken string
	chatID   string
}

// NewTelegramNotifier es la fábrica que crea nuestro mensajero
func NewTelegramNotifier(token, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: token,
		chatID:   chatID,
	}
}

// SendPush implementa nuestro puerto Notifier.
// Aquí hacemos el verdadero "Push": le empujamos los datos a los servidores de Telegram.
func (t *TelegramNotifier) SendPush(message string) error {
	// Construimos la URL de la API de Telegram usando nuestro token
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	// Preparamos el paquete de datos (Payload) que Telegram nos exige
	payload := map[string]string{
		"chat_id": t.chatID,
		"text":    message,
	}

	// Convertimos nuestro mapa de Go a formato JSON
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error empaquetando el JSON: %w", err)
	}

	// Hacemos una petición POST (enviar datos) a Telegram
	// Hacemos una petición POST
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("error de conexión con Telegram: %w", err)
	}
	defer resp.Body.Close()

	// NUEVO: Leemos la respuesta cruda de Telegram
	bodyBytes, _ := io.ReadAll(resp.Body)

	// Verificamos si Telegram aceptó nuestro mensaje
	if resp.StatusCode != http.StatusOK {
		// Ahora imprimiremos exactamente la queja de Telegram
		return fmt.Errorf("telegram rechazó el mensaje (código %d): %s", resp.StatusCode, string(bodyBytes))
	}

	log.Println("✅ Notificación push enviada con éxito a Telegram")
	return nil
}
