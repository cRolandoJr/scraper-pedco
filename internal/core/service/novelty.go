package service

import (
	"errors"
	"fmt"
	"html"
	"log"
	"regexp"
	"strconv"
	"strings"

	"scraper-pedco/internal/core/domain"
	"scraper-pedco/internal/core/ports"
)

const (
	kindForum         = "forum"
	kindGrade         = "grade"
	kindMaterial      = "material"
	forumPreviewRunes = 400
)

// composed es un mensaje en sus dos formas: Markdown legacy y el texto plano del reintento.
type composed struct{ markdown, plain string }

// novelty es un envío pendiente: sus claves se marcan vistas después de que sale.
type novelty struct {
	keys    []string
	message composed
}

// candidate es un ítem traído, antes de saber si ya se avisó.
type candidate struct {
	key      string
	novelty  novelty
	grade    domain.GradeItem
	material domain.Material
}

// notifyNovelties corre después de alertas, con el token que dejó esa fase. Una
// ErrSessionExpired corta las novedades del usuario sin re-loguear: la ronda siguiente lo resuelve.
func (notifier *Notifier) notifyNovelties(chatID int64, token string) {
	userID, courses, err := notifier.source.Profile(token)
	if err != nil {
		log.Printf("⚠️ ChatID %d: novedades sin perfil: %v", chatID, err)
		return
	}
	posts, forumErrs := notifier.source.Forums(token, courses)
	kinds := []struct {
		name  string
		fetch func(course domain.Course) ([]candidate, error)
	}{
		{kindForum, func(course domain.Course) ([]candidate, error) {
			if err := forumErrs[course.ID]; err != nil {
				return nil, err
			}
			return notifier.forumCandidates(course, posts[course.ID]), nil
		}},
		{kindGrade, func(course domain.Course) ([]candidate, error) {
			items, err := notifier.source.Grades(token, course.ID, userID)
			return gradeCandidates(items), err
		}},
		{kindMaterial, func(course domain.Course) ([]candidate, error) {
			materials, err := notifier.source.Materials(token, course.ID)
			return materialCandidates(materials), err
		}},
	}
	for _, kind := range kinds {
		for _, course := range courses {
			candidates, err := kind.fetch(course)
			if errors.Is(err, ports.ErrSessionExpired) {
				log.Printf("🔄 ChatID %d: token vencido en novedades; sigue la ronda siguiente", chatID)
				return
			}
			if err != nil {
				log.Printf("⚠️ ChatID %d: novedades de %s de %s: %v", chatID, kind.name, course.Name, err)
				continue
			}
			if notifier.deliverCourse(chatID, kind.name, course, candidates) {
				return
			}
		}
	}
}

// deliverCourse: sin línea de base marca todo y registra la base sin avisar;
// con base, avisa lo no visto y marca cada envío después de que sale. Devuelve
// true si Telegram rechazó el token del bot: hay que cortar las novedades del usuario.
func (notifier *Notifier) deliverCourse(chatID int64, kind string, course domain.Course, candidates []candidate) bool {
	hasBaseline, err := notifier.seenRepository.HasBaseline(chatID, kind, course.ID)
	if err != nil {
		log.Printf("⚠️ ChatID %d: no pude leer la base de %s de %s: %v", chatID, kind, course.Name, err)
		return false
	}
	if !hasBaseline {
		for _, item := range candidates {
			if err := notifier.seenRepository.MarkSeen(chatID, kind, item.key); err != nil {
				log.Printf("⚠️ ChatID %d: base de %s de %s incompleta, se rehace: %v", chatID, kind, course.Name, err)
				return false
			}
		}
		if err := notifier.seenRepository.SetBaseline(chatID, kind, course.ID); err != nil {
			log.Printf("⚠️ ChatID %d: no pude registrar la base de %s de %s: %v", chatID, kind, course.Name, err)
		}
		return false
	}

	var fresh []candidate
	for _, item := range candidates {
		seen, err := notifier.seenRepository.IsSeen(chatID, kind, item.key)
		if err != nil {
			log.Printf("⚠️ ChatID %d: no pude leer lo visto de %s: %v", chatID, kind, err)
			continue
		}
		if !seen {
			fresh = append(fresh, item)
		}
	}
	for _, pending := range notifier.compose(kind, course, fresh) {
		markSeen, cut := notifier.sendNovelty(chatID, pending)
		if cut {
			return true
		}
		if !markSeen {
			continue
		}
		for _, key := range pending.keys {
			if err := notifier.seenRepository.MarkSeen(chatID, kind, key); err != nil {
				log.Printf("⚠️ ChatID %d: salió el aviso de %s pero no pude marcarlo (se repetirá): %v", chatID, kind, err)
			}
		}
	}
	return false
}

// compose arma los envíos: uno por aviso; notas y material van en un mensaje por materia.
func (notifier *Notifier) compose(kind string, course domain.Course, fresh []candidate) []novelty {
	if len(fresh) == 0 {
		return nil
	}
	if kind == kindForum {
		novelties := make([]novelty, 0, len(fresh))
		for _, item := range fresh {
			novelties = append(novelties, item.novelty)
		}
		return novelties
	}
	merged := novelty{}
	var grades []domain.GradeItem
	var materials []domain.Material
	for _, item := range fresh {
		merged.keys = append(merged.keys, item.key)
		grades = append(grades, item.grade)
		materials = append(materials, item.material)
	}
	if kind == kindGrade {
		merged.message = formatGrades(course, grades)
	} else {
		merged.message = formatMaterials(course, materials)
	}
	return []novelty{merged}
}

// sendNovelty aplica las reglas de fallo de Telegram. markSeen: salió, o se rechazó de forma
// permanente en todas sus formas. cut: 401, el token del bot no sirve y no se manda nada más.
func (notifier *Notifier) sendNovelty(chatID int64, pending novelty) (markSeen, cut bool) {
	err := notifier.messageSender.Send(chatID, ports.ChannelNews, pending.message.markdown)
	if err == nil {
		return true, false
	}
	if errors.Is(err, ports.ErrSendUnauthorized) {
		log.Printf("🔒 ChatID %d: Telegram rechazó el token del bot; se cortan las novedades: %v", chatID, err)
		return false, true
	}
	if !errors.Is(err, ports.ErrSendPermanent) {
		log.Printf("⚠️ ChatID %d: falló el envío de la novedad, se reintenta: %v", chatID, err)
		return false, false
	}
	err = notifier.messageSender.SendPlain(chatID, ports.ChannelNews, pending.message.plain)
	if err == nil {
		return true, false
	}
	if errors.Is(err, ports.ErrSendUnauthorized) {
		log.Printf("🔒 ChatID %d: Telegram rechazó el token del bot; se cortan las novedades: %v", chatID, err)
		return false, true
	}
	if !errors.Is(err, ports.ErrSendPermanent) {
		log.Printf("⚠️ ChatID %d: falló el reenvío en texto plano, se reintenta: %v", chatID, err)
		return false, false
	}
	log.Printf("🗑️ ChatID %d: Telegram rechazó la novedad dos veces; descartado: %v", chatID, err)
	return true, false
}

func (notifier *Notifier) forumCandidates(course domain.Course, posts []domain.ForumPost) []candidate {
	candidates := make([]candidate, 0, len(posts))
	for _, post := range posts {
		candidates = append(candidates, candidate{
			key:     strconv.Itoa(post.Discussion),
			novelty: novelty{keys: []string{strconv.Itoa(post.Discussion)}, message: notifier.formatForumPost(course, post)},
		})
	}
	return candidates
}

// gradeCandidates: fuera el total del curso y lo sin nota. La clave es id + valor:
// una nota que cambia vuelve a avisar, y reescribir la fila sin cambiarla no.
func gradeCandidates(items []domain.GradeItem) []candidate {
	var candidates []candidate
	for _, item := range items {
		if item.Type == "course" || item.Raw == nil {
			continue
		}
		key := strconv.Itoa(item.ID) + ":" + strconv.FormatFloat(*item.Raw, 'g', -1, 64)
		candidates = append(candidates, candidate{key: key, grade: item})
	}
	return candidates
}

func materialCandidates(materials []domain.Material) []candidate {
	candidates := make([]candidate, 0, len(materials))
	for _, material := range materials {
		candidates = append(candidates, candidate{key: strconv.Itoa(material.CMID), material: material})
	}
	return candidates
}

// --- formato ---

var (
	htmlBreak   = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|li|h[1-6]|tr)>`)
	htmlTag     = regexp.MustCompile(`<[^>]*>`)
	inlineSpace = regexp.MustCompile(`[ \t\r\f\v\x{00a0}]+`)
)

// plainText saca el HTML: cierres de bloque a salto de línea, etiquetas fuera, entidades decodificadas.
func plainText(markup string) string {
	text := htmlBreak.ReplaceAllString(markup, "\n")
	text = htmlTag.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(inlineSpace.ReplaceAllString(line, " ")); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func cutRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func (notifier *Notifier) formatForumPost(course domain.Course, post domain.ForumPost) composed {
	subject, author := plainText(post.Subject), plainText(post.Author)
	preview := cutRunes(plainText(post.MessageHTML), forumPreviewRunes) // se corta ANTES de escapar
	published := notifier.formatPastDate(post.Published)
	body := func(escape func(string) string) string {
		text := fmt.Sprintf("📣 Aviso en %s\n%s\n— %s\n🗓 Publicado %s\n\n", escape(course.Name), escape(subject), escape(author), escape(published))
		if preview != "" {
			text += escape(preview) + "\n\n"
		}
		return text
	}
	return composed{
		markdown: body(markdownEscaper.Replace) + fmt.Sprintf("🔗 [Ver aviso](%s)", post.Link),
		plain:    body(asIs) + "🔗 Ver aviso: " + post.Link,
	}
}

func formatGrades(course domain.Course, items []domain.GradeItem) composed {
	body := func(escape func(string) string) string {
		var builder strings.Builder
		fmt.Fprintf(&builder, "📝 Nota nueva en %s\n", escape(course.Name))
		for _, item := range items {
			fmt.Fprintf(&builder, "%s: %s\n", escape(plainText(item.Name)), escape(plainText(item.Formatted)))
		}
		builder.WriteString("\n")
		return builder.String()
	}
	return composed{
		markdown: body(markdownEscaper.Replace) + fmt.Sprintf("🔗 [Ver notas](%s)", course.GradesLink),
		plain:    body(asIs) + "🔗 Ver notas: " + course.GradesLink,
	}
}

// formatMaterials: el encabezado una vez y una línea por material, como las notas.
func formatMaterials(course domain.Course, materials []domain.Material) composed {
	var markdown, plain strings.Builder
	fmt.Fprintf(&markdown, "📎 Material nuevo en %s", markdownEscaper.Replace(course.Name))
	fmt.Fprintf(&plain, "📎 Material nuevo en %s", course.Name)
	for _, material := range materials {
		name := plainText(material.Name)
		fmt.Fprintf(&markdown, "\n%s · 🔗 [Abrir](%s)", markdownEscaper.Replace(name), material.Link)
		fmt.Fprintf(&plain, "\n%s · 🔗 Abrir: %s", name, material.Link)
	}
	return composed{markdown: markdown.String(), plain: plain.String()}
}

func asIs(text string) string { return text }
