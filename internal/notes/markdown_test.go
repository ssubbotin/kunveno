package notes

import (
	"os"
	"strings"
	"testing"
	"time"
)

var msk = time.FixedZone("MSK", 3*3600)

func mskAt(h, m, s int) time.Time { return time.Date(2026, 10, 14, h, m, s, 0, msk) }

// meeting has its phrases out of order on purpose: rendering sorts them.
func meeting() Transcript {
	return Transcript{
		Slug: "ops", Room: "Планёрка", Start: mskAt(9, 37, 0), End: mskAt(10, 2, 0),
		Phrases: []Phrase{
			{Name: "Пётр", Start: mskAt(9, 50, 50), End: mskAt(9, 50, 58), Text: "Перерыв."},
			{Name: "Сергей", Start: mskAt(9, 37, 14), End: mskAt(9, 37, 20), Text: "Здесь какая-то есть смысловая нагрузка или нет?"},
			{Name: "Анна", Start: mskAt(9, 38, 1), End: mskAt(9, 38, 6), Text: "Это, скорее всего, было пожелание от Евгения."},
			{Name: "Сергей", Start: mskAt(9, 37, 5), End: mskAt(9, 37, 12), Text: "По файлам, то что-то вот эти."},
			{Name: "Анна", Start: mskAt(9, 39, 0), End: mskAt(9, 39, 3), Text: "Да."},           // 54 s after her last phrase
			{Name: "Пётр", Start: mskAt(9, 51, 12), End: mskAt(9, 51, 15), Text: "Продолжаем."}, // a pause lies between
		},
		Events: []Event{{Kind: KindPaused, At: mskAt(9, 51, 0)}, {Kind: KindResumed, At: mskAt(9, 51, 10)}},
	}
}

func TestMarkdownGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/planerka.ru.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(Markdown(meeting(), "ru", msk)); got != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownLabelsFollowTheLanguage(t *testing.T) {
	en := string(Markdown(meeting(), "en", msk))
	if !strings.HasPrefix(en, "# Meeting notes “Планёрка”\n") || !strings.Contains(en, "*09:51 recording paused*") || !strings.Contains(en, "*09:51 recording resumed*") {
		t.Fatalf("en:\n%s", en)
	}
	es := string(Markdown(meeting(), "es", msk))
	if !strings.HasPrefix(es, "# Notas de la reunión «Планёрка»\n") || !strings.Contains(es, "*09:51 grabación en pausa*") {
		t.Fatalf("es:\n%s", es)
	}
	if de := string(Markdown(meeting(), "de", msk)); !strings.HasPrefix(de, "# Заметки встречи") {
		t.Fatalf("unknown language did not fall back to Russian:\n%s", de)
	}
	if Lang("es") != "es" || Lang("") != "ru" || Lang("fr") != "ru" {
		t.Fatal("Lang")
	}
}

// Review focus: names and text with Markdown characters stay literal.
func TestMarkdownEscapesNames(t *testing.T) {
	tr := Transcript{Room: "R_&_D [old]", Start: mskAt(9, 0, 0), End: mskAt(9, 5, 0),
		Phrases: []Phrase{{Name: "Ann *admin*", Start: mskAt(9, 1, 0), End: mskAt(9, 1, 2), Text: "use `make` and _then_ [x]"}}}
	got := string(Markdown(tr, "en", msk))
	for _, want := range []string{`# Meeting notes “R\_&\_D \[old\]”`, `· Ann \*admin\*`, "**09:01 Ann \\*admin\\*:** use \\`make\\` and \\_then\\_ \\[x\\]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFileName(t *testing.T) {
	if got := FileName("ops", mskAt(9, 37, 0), msk); got != "ops-2026-10-14-0937.md" {
		t.Fatalf("FileName = %q", got)
	}
	if got := FileName("ops", mskAt(9, 37, 0), time.UTC); got != "ops-2026-10-14-0637.md" {
		t.Fatalf("FileName in UTC = %q", got)
	}
}
