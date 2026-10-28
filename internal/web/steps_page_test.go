package web

import (
	"net/http/httptest"
	"net/url"
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
	fillSteps(&v, testJournal(), -1, text, 0)
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
	fillSteps(&v, testJournal(), 0, text, 0)
	if len(v.Journal) != 13 || !v.Journal[0].CutStart || v.Journal[0].Long {
		t.Fatalf("все проходы: %d строк, первая %+v", len(v.Journal), v.Journal[0])
	}

	// «дольше 0,9 с» — шесть проходов шага 3 по 1 с (первый обрезан, но тоже идёт) и зависание;
	// последний проход шага 3 (0,4 с) и обычные 0,3 с шага 12 — нет
	v = stepsView{}
	fillSteps(&v, testJournal(), 900, text, 0)
	if len(v.Journal) != 7 {
		t.Fatalf("дольше 0,9 с: %d строк, ждали 7: %+v", len(v.Journal), v.Journal)
	}

	// Страница целиком: выбранный сигнал, подсветка зависания, «(5)» у шага 3
	v = stepsView{PLC: 1, File: "x.pla", Title: "Пресс", Sig: "Step",
		Signals: []sigOption{{Name: "Step", Selected: true}, {Name: "Code"}}}
	fillSteps(&v, testJournal(), -1, text, 0)
	w := httptest.NewRecorder()
	render(w, "steps", v)
	b := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, want := range []string{`value="Step" selected`, `class="warn-row"`, "Зажим закрыт, ждём стол", ">(5)</small>", "≥"} {
		if !strings.Contains(b, want) {
			t.Errorf("страница: нет %q", want)
		}
	}
}

// Режим работы: время по режимам; шаги — только в автомате, доля — от времени в автомате
func TestFillModes(t *testing.T) {
	// Режим: автомат (1) 0–6000 мс, ручной (2) 6000–9000, автомат 9000–14000
	mode := []steps.Step{
		{Value: 1, From: 0, To: 6000, CutStart: true},
		{Value: 2, From: 6000, To: 9000},
		{Value: 1, From: 9000, To: 14000, CutEnd: true},
	}
	modeText := func(n int64) string {
		return map[int64]string{1: "Автомат", 2: "Ручной"}[n]
	}
	none := func(int64) string { return "" }

	// Значение не выбрано: таблица режимов есть, журнал шагов не тронут
	var v stepsView
	j, base := fillModes(&v, mode, "", modeText, testJournal())
	if len(j) != 13 || base != 0 || len(v.Modes) != 2 || v.ModeNote != "" {
		t.Fatalf("без выбора: проходов %d, base %d, режимов %d, %q", len(j), base, len(v.Modes), v.ModeNote)
	}
	if v.Modes[0].Value != 1 || v.Modes[0].Total != "11 с" || v.Modes[0].Pct != "78,6 %" {
		t.Errorf("автомат: %+v", v.Modes[0])
	}
	if len(v.ModeVals) != 2 || v.ModeVals[0].Text != "Автомат" || v.ModeVals[0].Selected {
		t.Errorf("значения режима: %+v", v.ModeVals)
	}

	// Выбран автомат: зависание шага 12 (6200–11200) пересекает ручной режим —
	// от него остаются две обрезанные части, «необычно долгим» он больше не считается
	v = stepsView{}
	j, base = fillModes(&v, mode, "1", modeText, testJournal())
	if base != 11000 || !strings.Contains(v.ModeNote, "1 · Автомат") || !v.ModeVals[0].Selected {
		t.Fatalf("автомат: base %d, %q, %+v", base, v.ModeNote, v.ModeVals)
	}
	fillSteps(&v, j, -1, none, base)
	if len(v.Journal) != 0 {
		t.Errorf("в автомате зависание обрезано ручным режимом, а в журнале: %+v", v.Journal)
	}
	for _, st := range j {
		if st.From < 9000 && st.To > 6000 {
			t.Errorf("проход попал в ручной режим: %+v", st)
		}
	}

	// Режим, в котором цепочка не работала, — понятная ошибка, а не пустая страница
	v = stepsView{}
	j, base = fillModes(&v, []steps.Step{{Value: 2, From: 0, To: 14000}}, "2", modeText, nil)
	fillSteps(&v, j, -1, none, base)
	if v.Err != "в выбранном режиме цепочка не работала" {
		t.Errorf("пустой режим: %q", v.Err)
	}
}

// Ссылка из журнала: участок с запасом, курсоры на входе и выходе, сигналы шага и режима
func TestStepLink(t *testing.T) {
	v := &stepsView{PLC: 2, File: "2026/10/28/A_1.pla", Sig: "Шаги работы", Mode: "Режим работы"}

	// Короткий шаг (6 с): запас — минимальные 5 с
	u, err := url.Parse(stepLink(v, steps.Step{Value: 12, From: 100_000, To: 106_000}))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/view.html" || q.Get("plc") != "2" || q.Get("file") != "2026/10/28/A_1.pla" {
		t.Errorf("адрес: %s", u)
	}
	if q.Get("from") != "95000" || q.Get("to") != "111000" || q.Get("ca") != "100000" || q.Get("cb") != "106000" {
		t.Errorf("участок и курсоры: %v", q)
	}
	if s := q["s"]; len(s) != 2 || s[0] != "Шаги работы" || s[1] != "Режим работы" {
		t.Errorf("сигналы: %v", s)
	}

	// Долгий шаг (5 мин): запас — 10 % длительности, 30 с
	q = mustQuery(t, stepLink(v, steps.Step{From: 0, To: 300_000}))
	if q.Get("from") != "-30000" || q.Get("to") != "330000" {
		t.Errorf("запас у долгого шага: %v", q)
	}

	// Без режима — только сигнал шага
	v.Mode = ""
	if s := mustQuery(t, stepLink(v, steps.Step{From: 0, To: 1000}))["s"]; len(s) != 1 {
		t.Errorf("без режима: %v", s)
	}
}

func mustQuery(t *testing.T, link string) url.Values {
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}
