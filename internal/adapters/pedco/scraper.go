package pedco

import (
	"fmt"
	"log"
	"strings"

	"scraper-pedco/internal/core/domain"

	"github.com/gocolly/colly/v2"
)

type PedcoScraper struct {
	collector *colly.Collector
	baseURL   string
}

func NewPedcoScraper() *PedcoScraper {

	c := colly.NewCollector(
		// Los servidores web a veces bloquean bots. Le decimos a Colly que
		// finja ser un navegador Chrome normal de Windows para pasar desapercibido.
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Safari/537.36"),
	)

	return &PedcoScraper{
		collector: c,
		baseURL:   "https://pedco.uncoma.edu.ar",
	}
}

// Login implementa la primera parte de nuestro puerto (interfaz)
func (s *PedcoScraper) Login(username, password string) error {
	loginURL := s.baseURL + "/login/index.php"
	var loginToken string

	// REGLA 1: Le decimos a Colly qué hacer cuando vea cierta etiqueta HTML.
	// En Moodle, el token está en un input escondido: <input type="hidden" name="logintoken" value="texto_aleatorio">
	s.collector.OnHTML("input[name='logintoken']", func(e *colly.HTMLElement) {
		loginToken = e.Attr("value")
		log.Println("Token de seguridad capturado:", loginToken)
	})

	// REGLA 2: Ahora que Colly sabe qué buscar, visitamos la página.
	err := s.collector.Visit(loginURL)
	if err != nil {
		return fmt.Errorf("fallo al visitar la página de login: %w", err)
	}

	if loginToken == "" {
		return fmt.Errorf("no se pudo encontrar el token de login. ¿Cambió el diseño de Pedco?")
	}

	// REGLA 3: Enviamos nuestras credenciales junto con el token.
	// Colly guardará la cookie de sesión automáticamente de aquí en adelante.
	err = s.collector.Post(loginURL, map[string]string{
		"username":   username,
		"password":   password,
		"logintoken": loginToken,
	})

	if err != nil {
		return fmt.Errorf("error al enviar el formulario: %w", err)
	}

	log.Println("Login exitoso. El scraper ahora tiene acceso a tus cursos.")
	return nil
}

// FetchEvents navega a la página del calendario y extrae las fechas límite
func (s *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event
	calendarURL := s.baseURL + "/calendar/view.php?view=upcoming"

	// NUEVA ESTRATEGIA: Escaneamos todos los contenedores de tipo 'event'
	s.collector.OnHTML("div[data-type='event']", func(e *colly.HTMLElement) {

		title := e.ChildText("h3.name")
		titleLower := strings.ToLower(title) // Convertimos a minúsculas para comparar fácil
		component := e.Attr("data-event-component")

		// Definimos qué nos interesa capturar
		esTarea := component == "mod_assign"
		esCuestionario := component == "mod_quiz"
		esExamen := strings.Contains(titleLower, "parcial") ||
			strings.Contains(titleLower, "examen") ||
			strings.Contains(titleLower, "recuperatorio")

		// Si no es ninguna de las anteriores, ignoramos el evento
		if !esTarea && !esCuestionario && !esExamen {
			return
		}

		// Determinamos el tipo de etiqueta para el mensaje
		tipoEvento := "📌 Evento"
		if esTarea {
			tipoEvento = "📝 Tarea"
		} else if esCuestionario || esExamen {
			tipoEvento = "🔥 EXAMEN / PARCIAL"
		}

		eventID := e.Attr("data-event-id")
		courseName := e.ChildText("div.row a[href*='course/view.php']")
		linkEntrega := e.ChildAttr("div.card-footer a", "href")

		fechaCruda := e.DOM.Find("i.fa-clock-o").Closest(".row").Find(".col-11").Text()
		fechaLimpia := strings.TrimSpace(fechaCruda)

		event := domain.Event{
			ID:      eventID,
			Title:   title,
			Type:    tipoEvento,
			Course:  courseName,
			DueDate: fechaLimpia,
			Link:    linkEntrega,
		}

		events = append(events, event)
		log.Printf("✅ [%s] detectado: %s", tipoEvento, title)
	})

	log.Println("Visitando el calendario...")
	err := s.collector.Visit(calendarURL)
	if err != nil {
		return nil, fmt.Errorf("error al visitar el calendario: %w", err)
	}

	return events, nil
}
