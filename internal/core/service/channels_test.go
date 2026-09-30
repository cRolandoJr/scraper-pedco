package service

import (
	"strings"
	"testing"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

// T1: en la ronda la alerta va a Entregas; A, B y C (también su reenvío plano), a Novedades.
func TestChannels_RoundSendsAlertToDeliveriesAndNoveltiesToNews(t *testing.T) {
	_, source, sender, notifier := newsScenario(t)
	source.fetchResults[secretToken] = fetchResult{items: []domain.Item{pendingItem("TP ana")}}
	notifier.NotifyAll() // base de novedades; sale solo la alerta

	source.posts[10325] = []domain.ForumPost{post(1, "Aviso"), post(2, "Markdown_roto")}
	source.grades[10325] = []domain.GradeItem{grade(144101, "Entrega TP1", score(2), "2,00")}
	source.materials[10325] = []domain.Material{link(1, "resource", "Presentación")}
	sender.fail = func(message sentMessage) error {
		if message.kind == "markdown" && strings.Contains(message.message, "roto") {
			return errPermanent
		}
		return nil
	}
	notifier.NotifyAll()

	var alerts, forum, grades, materials, plain int
	for _, sent := range sender.sent {
		switch {
		case strings.Contains(sent.message, "TP ana"):
			alerts++
			if sent.channel != ports.ChannelDeliveries {
				t.Errorf("alerta en %q, quiero Entregas", sent.channel)
			}
			continue
		case sent.kind == "plain":
			plain++
		case strings.HasPrefix(sent.message, "📣"):
			forum++
		case strings.HasPrefix(sent.message, "📝"):
			grades++
		case strings.HasPrefix(sent.message, "📎"):
			materials++
		}
		if sent.channel != ports.ChannelNews {
			t.Errorf("novedad %s en %q, quiero Novedades: %q", sent.kind, sent.channel, sent.message)
		}
	}
	if alerts != 2 || forum != 2 || grades != 1 || materials != 1 || plain != 1 {
		t.Errorf("control positivo: alertas=%d A=%d B=%d C=%d plano=%d: %q", alerts, forum, grades, materials, plain, texts(sender.sent))
	}
}

// T7: el aviso 🔑 de credenciales rechazadas va fuera de tema.
func TestChannels_CredentialsRejectedGoesOutsideTopics(t *testing.T) {
	repository := newFakeRepository(ports.UserCredentials{ChatID: 1, User: "ana", Pass: "mala"})
	source := &fakeSource{loginResults: map[string]loginResult{"ana": {err: ports.ErrBadCredentials}}}
	sender := &fakeSender{}
	newTestNotifier(t, repository, source, sender).NotifyAll()

	if len(sender.sent) != 1 || sender.sent[0].message != keyMessage {
		t.Fatalf("control positivo: quiero UN 🔑: %q", texts(sender.sent))
	}
	if sender.sent[0].channel != ports.ChannelGeneral {
		t.Errorf("🔑 en %q, quiero fuera de tema", sender.sent[0].channel)
	}
}
