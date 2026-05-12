package pedco

import (
	"fmt"
	"log"

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
func (s *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event

	// Usualmente esta es la URL en Moodle para ver los eventos próximos.
	// Quizás tengas que ajustarla si en Pedco es ligeramente distinta (ej. /calendar/view.php?view=upcoming)
	calendarURL := s.baseURL + "/calendar/view.php?view=upcoming"

	// REGLA DE EXTRACCIÓN: Le decimos a Colly que busque el selector exacto que encontraste
	// li[data-region='event-item'][data-event-eventtype='due']
	// Esto significa: "Busca un elemento <li> que sea un ítem de evento Y que su tipo sea 'due' (vencimiento)"
	s.collector.OnHTML("li[data-region='event-item'][data-event-eventtype='due']", func(e *colly.HTMLElement) {

		// 1. Extraemos la URL del evento buscando el atributo "href" de la etiqueta <a> interna
		eventURL := e.ChildAttr("a", "href")

		// 2. Extraemos el ID único del evento
		eventID := e.ChildAttr("a", "data-event-id")

		// 3. Extraemos el texto visible (el título) buscando dentro del span con clase "eventname"
		title := e.ChildText("span.eventname")

		// Creamos nuestra entidad de Dominio con los datos raspados
		event := domain.Event{
			ID:     eventID,
			Title:  title,
			Type:   "TP/Vencimiento", // Sabemos que es un vencimiento por el 'due'
			Course: eventURL,         // Por ahora guardamos la URL aquí para tener el link directo
			// DueDate: ¡OJO AQUÍ!
		}

		// Agregamos este evento a nuestra lista
		events = append(events, event)
		log.Printf("Evento encontrado: %s", title)
	})

	// Ejecutamos la visita. Como ya hicimos Login antes, Colly enviará las cookies automáticamente.
	log.Println("Visitando el calendario...")
	err := s.collector.Visit(calendarURL)
	if err != nil {
		return nil, fmt.Errorf("error al visitar el calendario: %w", err)
	}

	return events, nil
}
