package notes

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// MergeGap: consecutive phrases of one speaker no further apart than this share a paragraph.
const MergeGap = 30 * time.Second

type labelSet struct{ title, paused, resumed, summary, auto string }

// labels are the fixed words of the file, in the language of the person who downloads it.
var labels = map[string]labelSet{
	"ru": {"Заметки встречи «%s»", "запись приостановлена", "запись возобновлена", "Итоги встречи «%s»",
		"Составлено автоматически по заметкам встречи, возможны ошибки."},
	"en": {"Meeting notes “%s”", "recording paused", "recording resumed", "Meeting summary “%s”",
		"Written automatically from the meeting notes; it may contain mistakes."},
	"es": {"Notas de la reunión «%s»", "grabación en pausa", "grabación reanudada", "Resumen de la reunión «%s»",
		"Redactado automáticamente a partir de las notas; puede contener errores."},
}

// Lang returns a language Markdown has labels for: ru, en or es; anything else is ru.
func Lang(s string) string {
	if _, ok := labels[s]; ok {
		return s
	}
	return "ru"
}

// escaper keeps names and recognised text literal in Markdown.
var escaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`,
	"&", `\&`, "~", `\~`)

// literal puts a name or text on one line (every run of whitespace or control characters becomes
// one space, so a line break cannot start a heading or a list) and escapes it.
func literal(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	return escaper.Replace(strings.Join(words, " "))
}

// Markdown renders a transcript: a title with the room, the date and time span with everyone who
// spoke, then one paragraph per run of one speaker and a line per pause or resumption (a pause at
// the very end is left out).
func Markdown(t Transcript, lang string, loc *time.Location) []byte {
	l := labels[Lang(lang)]
	var b strings.Builder
	fmt.Fprintf(&b, "# "+l.title+"\n\n", literal(t.Room))
	b.WriteString(t.Start.In(loc).Format("02.01.2006, 15:04") + "–" + t.End.In(loc).Format("15:04"))
	if names := speakers(t.Phrases); len(names) > 0 {
		for i := range names {
			names[i] = literal(names[i])
		}
		b.WriteString(" · " + strings.Join(names, ", "))
	}
	b.WriteString("\n")
	for _, p := range paragraphs(t) {
		b.WriteString("\n")
		clock := p.at.In(loc).Format("15:04")
		switch p.event {
		case KindPaused:
			fmt.Fprintf(&b, "*%s %s*\n", clock, l.paused)
		case KindResumed:
			fmt.Fprintf(&b, "*%s %s*\n", clock, l.resumed)
		default:
			fmt.Fprintf(&b, "**%s %s:** %s\n", clock, literal(p.name), literal(strings.Join(p.texts, " ")))
		}
	}
	return []byte(b.String())
}

// FileName is the download's name: the room and the meeting's start.
func FileName(slug string, start time.Time, loc *time.Location) string {
	return slug + "-" + start.In(loc).Format("2006-01-02-1504") + ".md"
}

type paragraph struct {
	at    time.Time
	event string // KindPaused or KindResumed; empty for speech
	name  string
	texts []string
	last  time.Time // end of the paragraph's last phrase
}

func sortedPhrases(ps []Phrase) []Phrase {
	out := append([]Phrase(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// speakers lists everyone who spoke, in order of their first phrase.
func speakers(ps []Phrase) []string {
	var names []string
	seen := map[string]bool{}
	for _, p := range sortedPhrases(ps) {
		if !seen[p.Name] {
			seen[p.Name] = true
			names = append(names, p.Name)
		}
	}
	return names
}

func paragraphs(t Transcript) []paragraph {
	type item struct {
		at     time.Time
		phrase *Phrase
		event  string
	}
	phrases := sortedPhrases(t.Phrases)
	items := make([]item, 0, len(phrases)+len(t.Events))
	for i := range phrases {
		items = append(items, item{at: phrases[i].Start, phrase: &phrases[i]})
	}
	for _, e := range t.Events {
		items = append(items, item{at: e.At, event: e.Kind})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	var out []paragraph
	for _, it := range items {
		if it.phrase == nil {
			out = append(out, paragraph{at: it.at, event: it.event})
			continue
		}
		p := it.phrase
		if n := len(out); n > 0 && out[n-1].event == "" && out[n-1].name == p.Name && p.Start.Sub(out[n-1].last) <= MergeGap {
			out[n-1].texts = append(out[n-1].texts, p.Text)
			if p.End.After(out[n-1].last) {
				out[n-1].last = p.End
			}
			continue
		}
		out = append(out, paragraph{at: p.Start, name: p.Name, texts: []string{p.Text}, last: p.End})
	}
	// A pause with no speech and no resumption after it is the end of the meeting ("Stop notes"),
	// not a break in it.
	for len(out) > 0 && out[len(out)-1].event == KindPaused {
		out = out[:len(out)-1]
	}
	return out
}

// SummaryMarkdown is the summary file: a title with the room, the meeting's date and time span, a
// note that it was written automatically, then the add-on's Markdown as it came.
func SummaryMarkdown(sm Summary, lang string, loc *time.Location) []byte {
	l := labels[Lang(lang)]
	var b strings.Builder
	fmt.Fprintf(&b, "# "+l.summary+"\n\n", literal(sm.Room))
	b.WriteString(sm.Start.In(loc).Format("02.01.2006, 15:04") + "–" + sm.End.In(loc).Format("15:04") + "\n\n")
	b.WriteString("*" + l.auto + "*\n\n")
	b.Write(sm.Body)
	return []byte(b.String())
}

// SummaryFileName is the summary download's name: the room, the meeting's start and "summary".
func SummaryFileName(slug string, start time.Time, loc *time.Location) string {
	return slug + "-" + start.In(loc).Format("2006-01-02-1504") + "-summary.md"
}
