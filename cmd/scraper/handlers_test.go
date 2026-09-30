package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tele "gopkg.in/telebot.v3"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
	"scraper-pedco/internal/core/service"
)

// stubSource: Login acepta cualquier cuenta; el resto no trae nada.
type stubSource struct{}

func (stubSource) Login(username, password string) (string, error) { return "tok", nil }
func (stubSource) FetchItems(token string, now time.Time) ([]domain.Item, error) {
	return nil, nil
}
func (stubSource) Profile(token string) (int, []domain.Course, error) { return 0, nil, nil }
func (stubSource) Forums(token string, courses []domain.Course) (map[int][]domain.ForumPost, map[int]error) {
	return nil, nil
}
func (stubSource) Grades(token string, courseID, userID int) ([]domain.GradeItem, error) {
	return nil, nil
}
func (stubSource) Materials(token string, courseID int) ([]domain.Material, error) {
	return nil, nil
}

// newTestBot: bot sin red, sincrónico, con los handlers reales contra el fake y la base temporal.
func newTestBot(t *testing.T, fake *fakeTelegram, repository ports.UserRepository) *tele.Bot {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	bot, err := tele.NewBot(tele.Settings{URL: server.URL, Token: botToken, Offline: true, Synchronous: true, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	notifier := service.NewNotifier(repository, nil, stubSource{}, newTelegramSender(bot, nil), time.UTC)
	registerHandlers(bot, notifier, newLoginFlow())
	return bot
}

func incoming(text string, threadID int) tele.Update {
	return tele.Update{ID: 1, Message: &tele.Message{
		ID: 1, Text: text, ThreadID: threadID,
		Sender: &tele.User{ID: 1},
		Chat:   &tele.Chat{ID: 1, Type: tele.ChatPrivate},
	}}
}

// T6: la respuesta va al tema donde escribió el usuario; fuera de tema, sin hilo.
func TestHandlers_ReplyInTheTopicTheUserWroteIn(t *testing.T) {
	conversations := map[string][]string{
		"/tps":    {"/tps"},
		"/login":  {"/login", "ana", "clave"},
		"ayuda":   {"hola"},
		"/start":  {"/start"},
		"/borrar": {"/borrar"},
	}
	for name, texts := range conversations {
		for _, threadID := range []int{184703, 0} {
			t.Run(fmt.Sprintf("%s/hilo=%d", name, threadID), func(t *testing.T) {
				repository := openTempStorage(t)
				fake := &fakeTelegram{reply: func(string) (int, string) { return 200, okMessage }}
				bot := newTestBot(t, fake, repository)
				for _, text := range texts {
					bot.ProcessUpdate(incoming(text, threadID))
				}
				replies := fake.calls("sendMessage")
				if len(replies) != len(texts) {
					t.Fatalf("control positivo: una respuesta por mensaje: %+v", replies)
				}
				want := ""
				if threadID != 0 {
					want = "184703"
				}
				for _, reply := range replies {
					if reply.threadID != want {
						t.Errorf("hilo %d: la respuesta va con %q, quiero %q: %s", threadID, reply.threadID, want, reply.text)
					}
				}
			})
		}
	}
}

// T8: /login de nuevo no borra los temas (no se crean duplicados); /borrar sí.
func TestHandlers_LoginKeepsTopicsAndBorrarDeletesThem(t *testing.T) {
	repository := openTempStorage(t)
	if err := repository.SaveUser(1, "ana", "vieja"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveTopic(1, ports.ChannelDeliveries, 11); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTelegram{replyTo: func(entry recorded) (int, string) {
		if entry.method == "createForumTopic" {
			return 200, topicCreated(99)
		}
		return 200, okMessage
	}}
	bot := newTestBot(t, fake, repository)

	for _, text := range []string{"/login", "ana", "nueva"} {
		bot.ProcessUpdate(incoming(text, 0))
	}
	if replies := fake.calls("sendMessage"); len(replies) != 3 || !strings.Contains(replies[2].text, "Listo") {
		t.Fatalf("control positivo: el /login terminó bien: %+v", replies)
	}
	if user, err := repository.GetUser(1); err != nil || user.Pass != "nueva" {
		t.Fatalf("control positivo: SaveUser corrió: %+v %v", user, err)
	}
	if threadID, found, err := repository.TopicID(1, ports.ChannelDeliveries); err != nil || !found || threadID != 11 {
		t.Fatalf("/login borró el tema: %d %v %v", threadID, found, err)
	}
	sender := newTopicSender(t, fake, repository)
	if err := sender.Send(1, ports.ChannelDeliveries, "alerta"); err != nil {
		t.Fatal(err)
	}
	if creates := fake.calls("createForumTopic"); len(creates) != 0 {
		t.Errorf("tras /login no se duplica el tema: %+v", creates)
	}

	bot.ProcessUpdate(incoming("/borrar", 0))
	if replies := fake.calls("sendMessage"); !strings.Contains(replies[len(replies)-1].text, "eliminadas") {
		t.Fatalf("control positivo: /borrar borró: %+v", replies[len(replies)-1])
	}
	if _, found, _ := repository.TopicID(1, ports.ChannelDeliveries); found {
		t.Errorf("/borrar no borró el tema")
	}
}
