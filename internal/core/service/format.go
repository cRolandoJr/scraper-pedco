package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"scraper-pedco/internal/core/domain"
)

var weekdayNames = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

// Markdown legacy de Telegram: no hay escape dentro de una entidad, así que el
// texto de la cátedra va siempre fuera de *…* y con estos cuatro escapados.
var markdownEscaper = strings.NewReplacer("_", `\_`, "*", `\*`, "`", "\\`", "[", `\[`)

func (notifier *Notifier) formatItems(items []domain.Item, automaticRound bool) string {
	var pending, done []domain.Item
	for _, item := range items {
		if item.Status == domain.Done {
			done = append(done, item)
		} else {
			pending = append(pending, item)
		}
	}
	byDue := func(list []domain.Item) {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Due.Before(list[j].Due) })
	}
	byDue(pending)
	byDue(done)

	var builder strings.Builder
	if automaticRound {
		builder.WriteString("⏰ *Alerta Automática de Entregas:*\n\n")
	} else {
		builder.WriteString("📚 *Tus Próximos Eventos en Pedco:*\n\n")
	}
	for _, item := range pending {
		kindLabel := "📝 Tarea"
		if item.Kind == domain.Quiz {
			kindLabel = "🔥 Examen"
		}
		fmt.Fprintf(&builder, "%s: %s\n📘 %s\n⏰ %s\n%s\n🔗 [Ir a Pedco](%s)\n\n",
			kindLabel, markdownEscaper.Replace(item.Title), markdownEscaper.Replace(item.Course),
			notifier.dateLine(item), notifier.statusLine(item), item.Link)
	}
	if len(done) > 0 {
		builder.WriteString("✅ *Hecho*\n")
		for _, item := range done {
			fmt.Fprintf(&builder, "✅ %s · %s\n", markdownEscaper.Replace(item.Title), markdownEscaper.Replace(item.Course))
		}
		builder.WriteString("\n")
	}
	builder.WriteString(notifier.signatureBlock)
	return builder.String()
}

func (notifier *Notifier) dateLine(item domain.Item) string {
	if item.Kind == domain.Assignment {
		return "Vence " + notifier.formatDate(item.Due)
	}
	switch {
	case item.OpensAt.IsZero():
		return "Cierra " + notifier.formatDate(item.Due)
	case item.OpensAt.Equal(item.Due): // quiz sin cierre: Due = timeopen
		return "Abre " + notifier.formatDate(item.OpensAt)
	default:
		return "Abre " + notifier.formatDate(item.OpensAt) + " · cierra " + notifier.formatDate(item.Due)
	}
}

func (notifier *Notifier) statusLine(item domain.Item) string {
	switch item.Status {
	case domain.Draft:
		return "⏳ Borrador sin enviar"
	case domain.InProgress:
		return "⏳ Intento sin terminar"
	case domain.Unknown:
		return "❔ No pude leer el estado"
	case domain.NotOpen:
		if item.Kind == domain.Quiz {
			return "🔒 Todavía no abrió"
		}
		return "🔒 Abre " + notifier.formatDate(item.OpensAt)
	default:
		return "⏳ Pendiente"
	}
}

// formatDate: "Hoy 23:55", "Mañana 23:59" o "lun 05/10 22:00", en hora AR.
func (notifier *Notifier) formatDate(moment time.Time) string {
	local := moment.In(notifier.location)
	today := notifier.now().In(notifier.location)
	clock := local.Format("15:04")
	switch {
	case sameDay(local, today):
		return "Hoy " + clock
	case sameDay(local, today.AddDate(0, 0, 1)):
		return "Mañana " + clock
	default:
		return fmt.Sprintf("%s %s %s", weekdayNames[local.Weekday()], local.Format("02/01"), clock)
	}
}

func sameDay(first, second time.Time) bool {
	return first.Year() == second.Year() && first.YearDay() == second.YearDay()
}
