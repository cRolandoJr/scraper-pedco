package pedco

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"

	"github.com/gocolly/colly/v2"
)

const (
	baseURL        = "https://pedco.uncoma.edu.ar"
	requestTimeout = 15 * time.Second
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Safari/537.36"
)

// ErrSessionExpired re-exporta el sentinel del puerto para que callers que ya
// importan este adapter no necesiten conocer la capa ports.
var ErrSessionExpired = ports.ErrSessionExpired

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

// SessionBlob serializa las cookies actuales del scraper para persistir.
func (s *PedcoScraper) SessionBlob() (string, error) {
	cookies := s.collector.Cookies(baseURL)
	if len(cookies) == 0 {
		return "", errors.New("scraper sin cookies para serializar")
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadSession inyecta cookies previas. Llamar antes de FetchEvents para evitar login.
func (s *PedcoScraper) LoadSession(blob string) error {
	if blob == "" {
		return errors.New("sesión vacía")
	}
	var cookies []*http.Cookie
	if err := json.Unmarshal([]byte(blob), &cookies); err != nil {
		return fmt.Errorf("blob de sesión corrupto: %w", err)
	}
	u, _ := url.Parse(baseURL)
	return s.collector.SetCookies(u.String(), cookies)
}

func (s *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event
	var redirectedToLogin bool
	calendarURL := baseURL + "/calendar/view.php?view=upcoming"

	pageCollector := s.collector.Clone()

	// Si Moodle redirige a /login, la sesión está muerta.
	pageCollector.OnResponse(func(r *colly.Response) {
		if strings.Contains(r.Request.URL.Path, "/login/") {
			redirectedToLogin = true
		}
	})

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
	if redirectedToLogin {
		return nil, ports.ErrSessionExpired
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
