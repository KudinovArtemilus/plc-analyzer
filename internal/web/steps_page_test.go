package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// Журнал из 13 проходов: шаг 3 по 1 с, шаг 12 обычно 0,3 с и одно зависание на 5 с
func testJournal() []steps.Step {
	var j []steps.Step
	t := int64(0)
	add := func(v, d int64) {
		j = append(j, steps.Step{Value: v, From: t, To: t + d})
		t += d
	}
	for i := 0; i < 6; i++ {
		add(3, 1000)
		if i == 4 {
			add(12, 5000)
		} else {
			add(12, 300)
		}
	}
	add(3, 400)
	j[0].CutStart = true
	j[len(j)-1].CutEnd = true
	return j
}

func TestFillSteps(t *testing.T) {
	text := func(n int64) string {
		if n == 12 {
			return "Зажим закрыт, ждём стол"
		}
		return ""
	}

	// Без фильтра — только необычно долгие: одно зависание шага 12
	var v stepsView
	fillSteps(&v, testJournal(), -1, text)
	if v.Passes != 13 || len(v.Summary) != 2 || v.Summary[0].Value != 12 {
		t.Fatalf("сводка: %+v", v.Summary)
	}
	if v.Summary[0].Median != "300 мс" || v.Summary[0].Text != "Зажим закрыт, ждём стол" {
		t.Errorf("шаг 12: %+v", v.Summary[0])
	}
	// У шага 3 два прохода обрезаны краями — «Partial»
	if s3 := v.Summary[1]; !s3.Partial || s3.Count != 7 || s3.Full != 5 {
		t.Errorf("шаг 3: %+v", s3)
	}
	if len(v.Journal) != 1 || v.Journal[0].Value != 12 || v.Journal[0].Dur != "5 с" || !v.Journal[0].Long {
		t.Fatalf("необычно долгие: %+v", v.Journal)
	}
	if !strings.Contains(v.JournalNote, "1 из 13") {
		t.Errorf("подпись журнала: %q", v.JournalNote)
	}

	// «0» — все проходы; первый обрезан началом записи
	v = stepsView{}
	fillSteps(&v, testJournal(), 0, text)
	if len(v.Journal) != 13 || !v.Journal[0].CutStart || v.Journal[0].Long {
		t.Fatalf("все проходы: %d строк, первая %+v", len(v.Journal), v.Journal[0])
	}

	// «дольше 0,9 с» — шесть проходов шага 3 по 1 с (первый обрезан, но тоже идёт) и зависание;
	// последний проход шага 3 (0,4 с) и обычные 0,3 с шага 12 — нет
	v = stepsView{}
	fillSteps(&v, testJournal(), 900, text)
	if len(v.Journal) != 7 {
		t.Fatalf("дольше 0,9 с: %d строк, ждали 7: %+v", len(v.Journal), v.Journal)
	}

	// Страница целиком: выбранный сигнал, подсветка зависания, «(5)» у шага 3
	v = stepsView{PLC: 1, File: "x.pla", Title: "Пресс", Sig: "Step",
		Signals: []sigOption{{Name: "Step", Selected: true}, {Name: "Code"}}}
	fillSteps(&v, testJournal(), -1, text)
	w := httptest.NewRecorder()
	render(w, "steps", v)
	b := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{`value="Step" selected`, `class="warn-row"`, "Зажим закрыт, ждём стол", ">(5)</small>", "≥"} {
		if !strings.Contains(b, want) {
			t.Errorf("страница: нет %q", want)
		}
	}
}
