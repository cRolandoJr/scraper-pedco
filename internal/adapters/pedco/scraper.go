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
	collector := colly.NewCollector(colly.UserAgent(userAgent))
	collector.SetRequestTimeout(requestTimeout)
	return &PedcoScraper{collector: collector}
}

func (scraper *PedcoScraper) Login(username, password string) error {
	loginURL := baseURL + "/login/index.php"

	var loginToken string
	scraper.collector.OnHTML("input[name='logintoken']", func(htmlElement *colly.HTMLElement) {
		loginToken = htmlElement.Attr("value")
	})

	if err := scraper.collector.Visit(loginURL); err != nil {
		return fmt.Errorf("fallo al visitar página de login: %w", err)
	}
	if loginToken == "" {
		return fmt.Errorf("no se pudo encontrar logintoken (¿cambió layout de Pedco?)")
	}

	err := scraper.collector.Post(loginURL, map[string]string{
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
func (scraper *PedcoScraper) SessionBlob() (string, error) {
	cookies := scraper.collector.Cookies(baseURL)
	if len(cookies) == 0 {
		return "", errors.New("scraper sin cookies para serializar")
	}
	serialized, err := json.Marshal(cookies)
	if err != nil {
		return "", err
	}
	return string(serialized), nil
}

// LoadSession inyecta cookies previas. Llamar antes de FetchEvents para evitar login.
func (scraper *PedcoScraper) LoadSession(sessionBlob string) error {
	if sessionBlob == "" {
		return errors.New("sesión vacía")
	}
	var cookies []*http.Cookie
	if err := json.Unmarshal([]byte(sessionBlob), &cookies); err != nil {
		return fmt.Errorf("blob de sesión corrupto: %w", err)
	}
	parsedBaseURL, _ := url.Parse(baseURL)
	return scraper.collector.SetCookies(parsedBaseURL.String(), cookies)
}

func (scraper *PedcoScraper) FetchEvents() ([]domain.Event, error) {
	var events []domain.Event
	var redirectedToLogin bool
	calendarURL := baseURL + "/calendar/view.php?view=upcoming"

	pageCollector := scraper.collector.Clone()

	// Si Moodle redirige a /login, la sesión está muerta.
	pageCollector.OnResponse(func(response *colly.Response) {
		if strings.Contains(response.Request.URL.Path, "/login/") {
			redirectedToLogin = true
		}
	})

	pageCollector.OnHTML("div[data-type='event']", func(htmlElement *colly.HTMLElement) {
		event, isInteresting := parseEvent(htmlElement)
		if !isInteresting {
			return
		}
		events = append(events, event)
		log.Printf("✅ [%s] %s", event.Type, event.Title)
	})

	if err := pageCollector.Visit(calendarURL); err != nil {
		return nil, fmt.Errorf("error visitando calendario: %w", err)
	}
	if redirectedToLogin {
		return nil, ports.ErrSessionExpired
	}
	return events, nil
}

func parseEvent(htmlElement *colly.HTMLElement) (domain.Event, bool) {
	title := htmlElement.ChildText("h3.name")
	titleLowercase := strings.ToLower(title)
	moodleComponent := htmlElement.Attr("data-event-component")

	isAssignment := moodleComponent == "mod_assign"
	isQuiz := moodleComponent == "mod_quiz"
	isExam := strings.Contains(titleLowercase, "parcial") ||
		strings.Contains(titleLowercase, "examen") ||
		strings.Contains(titleLowercase, "recuperatorio")

	if !isAssignment && !isQuiz && !isExam {
		return domain.Event{}, false
	}

	eventType := "📌 Evento"
	switch {
	case isAssignment:
		eventType = "📝 Tarea"
	case isQuiz || isExam:
		eventType = "🔥 EXAMEN / PARCIAL"
	}

	rawDueDate := htmlElement.DOM.Find("i.fa-clock-o").Closest(".row").Find(".col-11").Text()

	return domain.Event{
		ID:      htmlElement.Attr("data-event-id"),
		Title:   title,
		Type:    eventType,
		Course:  htmlElement.ChildText("div.row a[href*='course/view.php']"),
		DueDate: strings.TrimSpace(rawDueDate),
		Link:    htmlElement.ChildAttr("div.card-footer a", "href"),
	}, true
}
