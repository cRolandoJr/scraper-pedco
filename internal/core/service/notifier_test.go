package service

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

// ---- Mocks ----

type fakeRepo struct {
	all     []ports.UserCredentials
	one     map[int64][2]string // chatID -> [user, pass]
	allErr  error
	oneErr  error
}

func (r *fakeRepo) GetUser(chatID int64) (string, string, error) {
	if r.oneErr != nil {
		return "", "", r.oneErr
	}
	v, ok := r.one[chatID]
	if !ok {
		return "", "", errors.New("not found")
	}
	return v[0], v[1], nil
}

func (r *fakeRepo) GetAllUsers() ([]ports.UserCredentials, error) {
	return r.all, r.allErr
}

type fakeScraper struct {
	loginErr error
	events   []domain.Event
	fetchErr error
	loggedIn bool
}

func (s *fakeScraper) Login(user, pass string) error {
	if s.loginErr != nil {
		return s.loginErr
	}
	s.loggedIn = true
	return nil
}

func (s *fakeScraper) FetchEvents() ([]domain.Event, error) {
	if !s.loggedIn {
		return nil, errors.New("not logged in")
	}
	return s.events, s.fetchErr
}

type fakeSender struct {
	mu       sync.Mutex
	messages map[int64][]string
}

func newFakeSender() *fakeSender {
	return &fakeSender{messages: make(map[int64][]string)}
}

func (s *fakeSender) Send(chatID int64, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[chatID] = append(s.messages[chatID], msg)
	return nil
}

// ---- Tests ----

func TestNotifyOne_NoCredentials(t *testing.T) {
	repo := &fakeRepo{one: map[int64][2]string{}}
	factory := ports.ScraperFactory(func() ports.Scraper { return &fakeScraper{} })
	n := NewNotifier(repo, factory, newFakeSender())

	_, err := n.NotifyOne(123)
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestNotifyOne_NoEventsReturnsEmpty(t *testing.T) {
	repo := &fakeRepo{one: map[int64][2]string{1: {"u", "p"}}}
	factory := ports.ScraperFactory(func() ports.Scraper {
		return &fakeScraper{events: nil}
	})
	n := NewNotifier(repo, factory, newFakeSender())

	msg, err := n.NotifyOne(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "" {
		t.Fatalf("expected empty msg, got %q", msg)
	}
}

func TestNotifyOne_FormatsEvents(t *testing.T) {
	repo := &fakeRepo{one: map[int64][2]string{1: {"u", "p"}}}
	factory := ports.ScraperFactory(func() ports.Scraper {
		return &fakeScraper{events: []domain.Event{{
			ID: "1", Title: "TP1", Type: "📝 Tarea", Course: "Algebra",
			DueDate: "mañana", Link: "http://x",
		}}}
	})
	n := NewNotifier(repo, factory, newFakeSender())
	n.delayBetween = 0

	msg, err := n.NotifyOne(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "TP1") || !strings.Contains(msg, "Algebra") {
		t.Fatalf("msg missing event fields: %q", msg)
	}
	if !strings.Contains(msg, "Próximos Eventos") {
		t.Fatal("expected manual header")
	}
}

func TestNotifyAll_SkipsUsersWithoutEvents(t *testing.T) {
	repo := &fakeRepo{all: []ports.UserCredentials{
		{ChatID: 1, User: "a", Pass: "x"},
		{ChatID: 2, User: "b", Pass: "y"},
	}}
	call := 0
	factory := ports.ScraperFactory(func() ports.Scraper {
		call++
		if call == 1 {
			return &fakeScraper{events: nil}
		}
		return &fakeScraper{events: []domain.Event{{
			Title: "Examen", Type: "🔥 EXAMEN / PARCIAL", Course: "Mate",
		}}}
	})
	sender := newFakeSender()
	n := NewNotifier(repo, factory, sender)
	n.delayBetween = 0

	n.NotifyAll()

	if got := len(sender.messages[1]); got != 0 {
		t.Errorf("user 1 sin eventos no debe recibir mensajes, got %d", got)
	}
	if got := len(sender.messages[2]); got != 1 {
		t.Errorf("user 2 debe recibir 1 mensaje, got %d", got)
	}
	if !strings.Contains(sender.messages[2][0], "Alerta Automática") {
		t.Error("mensaje automático debe usar header automático")
	}
}

func TestNotifyAll_ContinuesOnLoginFailure(t *testing.T) {
	repo := &fakeRepo{all: []ports.UserCredentials{
		{ChatID: 1, User: "a", Pass: "x"},
		{ChatID: 2, User: "b", Pass: "y"},
	}}
	call := 0
	factory := ports.ScraperFactory(func() ports.Scraper {
		call++
		if call == 1 {
			return &fakeScraper{loginErr: errors.New("bad creds")}
		}
		return &fakeScraper{events: []domain.Event{{Title: "T", Type: "📝 Tarea"}}}
	})
	sender := newFakeSender()
	n := NewNotifier(repo, factory, sender)
	n.delayBetween = 0

	n.NotifyAll()

	if len(sender.messages[2]) != 1 {
		t.Errorf("user 2 debe recibir mensaje pese a fallo de user 1, got %d", len(sender.messages[2]))
	}
}
