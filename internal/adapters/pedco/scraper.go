package pedco

import (
	"fmt"
	"log"
	"strings"

	"scraper-pedco/internal/core/domain"

	"github.com/gocolly/colly/v2"
)

// PedcoScraper es nuestro adaptador que interactúa con la web de la universidad
type PedcoScraper struct {
	collector *colly.Collector
	baseURL   string
}

// NewPedcoScraper es el constructor (factory) de nuestro adaptador
func NewPedcoScraper() *PedcoScraper {
	// Inicializamos Colly
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
// FetchEvents navega a la página del calendario y extrae las fechas límite
func (s *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event
	calendarURL := s.baseURL + "/calendar/view.php?view=upcoming"

	// NUEVA REGLA DE EXTRACCIÓN: Buscamos el div contenedor del evento
	s.collector.OnHTML("div[data-type='event'][data-event-component='mod_assign']", func(e *colly.HTMLElement) {

		eventID := e.Attr("data-event-id")
		title := e.ChildText("h3.name")
		courseName := e.ChildText("div.row a[href*='course/view.php']")
		linkEntrega := e.ChildAttr("div.card-footer a", "href")

		// NUEVO: La magia de GoQuery a través de Colly
		// 1. Busca el relojito (i.fa-clock-o)
		// 2. Sube a la fila contenedora (.Closest(".row"))
		// 3. Busca la columna con el texto (.Find(".col-11"))
		// 4. Extrae todo el texto adentro (.Text())
		fechaCruda := e.DOM.Find("i.fa-clock-o").Closest(".row").Find(".col-11").Text()

		// Pro-tip: El HTML web suele traer saltos de línea ocultos o espacios de sobra.
		// TrimSpace lo deja limpio.
		fechaLimpia := strings.TrimSpace(fechaCruda)

		event := domain.Event{
			ID:      eventID,
			Title:   title,
			Type:    "Trabajo Práctico",
			Course:  courseName,
			DueDate: fechaLimpia, // Asignamos la fecha real
			Link:    linkEntrega, // Asignamos el link real
		}

		events = append(events, event)
		log.Printf("✅ Evento capturado: %s - Vence: %s", title, fechaLimpia)
	})

	log.Println("Visitando el calendario...")
	err := s.collector.Visit(calendarURL)
	if err != nil {
		return nil, fmt.Errorf("error al visitar el calendario: %w", err)
	}

	return events, nil
}
