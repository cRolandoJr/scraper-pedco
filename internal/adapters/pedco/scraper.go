package pedco

import (
	"fmt"
	"log"
	"strings"
	"time"

	"scraper-pedco/internal/core/domain"

	"github.com/gocolly/colly/v2"
)

const (
	baseURL        = "https://pedco.uncoma.edu.ar"
	requestTimeout = 15 * time.Second
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Safari/537.36"
)

type PedcoScraper struct {
	collector *colly.Collector
}

func NewPedcoScraper() *PedcoScraper {
	c := colly.NewCollector(colly.UserAgent(userAgent))
	c.SetRequestTimeout(requestTimeout)
	return &PedcoScraper{collector: c}
}

func (s *PedcoScraper) Login(username, password string) error {
	loginURL := baseURL + "/login/index.php"

	var loginToken string
	s.collector.OnHTML("input[name='logintoken']", func(e *colly.HTMLElement) {
		loginToken = e.Attr("value")
	})

	if err := s.collector.Visit(loginURL); err != nil {
		return fmt.Errorf("fallo al visitar página de login: %w", err)
	}
	if loginToken == "" {
		return fmt.Errorf("no se pudo encontrar logintoken (¿cambió layout de Pedco?)")
	}

	err := s.collector.Post(loginURL, map[string]string{
		"username":   username,
		"password":   password,
		"logintoken": loginToken,
	})
	if err != nil {
		return fmt.Errorf("error enviando formulario login: %w", err)
	}
	log.Println("Login exitoso.")
	return nil
}

func (s *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event
	calendarURL := baseURL + "/calendar/view.php?view=upcoming"

	// Clone hereda cookies de sesión pero aísla el callback OnHTML
	// para no acumular handlers entre invocaciones repetidas.
	pageCollector := s.collector.Clone()
	pageCollector.OnHTML("div[data-type='event']", func(e *colly.HTMLElement) {
		ev, ok := parseEvent(e)
		if !ok {
			return
		}
		events = append(events, ev)
		log.Printf("✅ [%s] %s", ev.Type, ev.Title)
	})

	if err := pageCollector.Visit(calendarURL); err != nil {
		return nil, fmt.Errorf("error visitando calendario: %w", err)
	}
	return events, nil
}

func parseEvent(e *colly.HTMLElement) (domain.Event, bool) {
	title := e.ChildText("h3.name")
	titleLower := strings.ToLower(title)
	component := e.Attr("data-event-component")

	esTarea := component == "mod_assign"
	esCuestionario := component == "mod_quiz"
	esExamen := strings.Contains(titleLower, "parcial") ||
		strings.Contains(titleLower, "examen") ||
		strings.Contains(titleLower, "recuperatorio")

	if !esTarea && !esCuestionario && !esExamen {
		return domain.Event{}, false
	}

	tipo := "📌 Evento"
	switch {
	case esTarea:
		tipo = "📝 Tarea"
	case esCuestionario || esExamen:
		tipo = "🔥 EXAMEN / PARCIAL"
	}

	fechaCruda := e.DOM.Find("i.fa-clock-o").Closest(".row").Find(".col-11").Text()

	return domain.Event{
		ID:      e.Attr("data-event-id"),
		Title:   title,
		Type:    tipo,
		Course:  e.ChildText("div.row a[href*='course/view.php']"),
		DueDate: strings.TrimSpace(fechaCruda),
		Link:    e.ChildAttr("div.card-footer a", "href"),
	}, true
}
