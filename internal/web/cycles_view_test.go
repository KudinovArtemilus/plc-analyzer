package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/cycles"
	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// cycleJournal — исходный шаг 0 (ждём 20 с), цикл 10 → 12 → 14; в третьем цикле шаг 12 завис
func cycleJournal() []steps.Step {
	var j []steps.Step
	var t int64
	add := func(v, d int64) {
		j = append(j, steps.Step{Value: v, From: t, To: t + d})
		t += d
	}
	for i := 0; i < 5; i++ {
		add(0, 20000)
		add(10, 2000)
		if i == 2 {
			add(12, 60000)
		} else {
			add(12, 1000)
		}
		add(14, 4000)
	}
	add(0, 5000)
	j[0].CutStart = true
	j[len(j)-1].CutEnd = true
	return j
}

func TestHomeByText(t *testing.T) {
	sum := []steps.Stat{{Value: 14, Count: 9}, {Value: 3, Count: 9}, {Value: 21, Count: 1}}
	text := func(n int64) string {
		return map[int64]string{14: "Обработка", 3: "Исходное", 21: "Базовая позиция"}[n]
	}
	// 14 дольше всего, но подпись «Исходное» у шага 3; 21 — «Базовая», но был один раз
	if h, ok := homeByText(sum, text); !ok || h != 3 {
		t.Errorf("по подписи: %d, %v; ждали 3", h, ok)
	}
	if _, ok := homeByText(sum, func(int64) string { return "" }); ok {
		t.Error("без подписей кандидата по тексту быть не должно")
	}
}

func TestFillCycles(t *testing.T) {
	// Пять циклов мало для тренда (нужно 6) — график есть, подписи тренда нет; проверяем и это
	text := func(n int64) string {
		return map[int64]string{0: "Базовая позиция", 12: "Зажим закрыт"}[n]
	}
	j := cycleJournal()
	sum := steps.Summary(j)
	base := j[len(j)-1].To - j[0].From

	// Исходный шаг не выбран — программа предлагает 0 (там дольше всего стоит и возвращается)
	v := stepsView{PLC: 1, File: "f.pla", Sig: "Step"}
	fillCycles(&v, j, sum, "", base, text)
	c := v.Cycles
	if !c.HomeAuto || c.HomeName != "0 · Базовая позиция" {
		t.Fatalf("исходный шаг: %+v", c)
	}
	if c.Count != 5 || c.Median != "7 с" || c.MaxDur != "1 мин 06 с" {
		t.Errorf("сводка: циклов %d, медиана %q, максимум %q", c.Count, c.Median, c.MaxDur)
	}
	// Самый долгий — третий: зависание шага 12, выделен, ссылка на график
	l := c.Longest[0]
	if l.Dur != "1 мин 06 с" || !l.Long || !strings.HasPrefix(l.Slow, "12 · Зажим закрыт: 1 мин 00 с (обычно 1 с)") ||
		!strings.Contains(l.Link, "ca=") {
		t.Errorf("самый долгий: %+v", l)
	}
	if c.Longest[1].Long || c.Longest[1].Slow != "" {
		t.Errorf("обычный цикл выделен как долгий или с «затянувшимся» шагом: %+v", c.Longest[1])
	}

	// Исходным выбран не тот шаг (12): «циклы» всё равно найдутся — 12 → 14 → 0 → 10 → 12,
	// но ожидание в базовой позиции (20 с) окажется ВНУТРИ цикла, а зависание шага 12 —
	// в «ожидании». Поэтому исходный шаг важно выбрать правильно.
	v = stepsView{}
	fillCycles(&v, j, sum, "12", base, text)
	if v.Cycles.HomeAuto || v.Cycles.Count != 4 || v.Cycles.Median != "26 с" {
		t.Errorf("исходный 12: %+v", v.Cycles)
	}

	// Шаг, который был один раз, — циклов между возвратами нет, понятная подпись
	v = stepsView{}
	one := []steps.Step{{Value: 3, From: 0, To: 1000, CutStart: true}, {Value: 5, From: 1000, To: 2000, CutEnd: true}}
	fillCycles(&v, one, steps.Summary(one), "", 2000, text)
	if v.Cycles.Note == "" || v.Cycles.Count != 0 {
		t.Errorf("без возвратов: %+v", v.Cycles)
	}

	// Страница: выбор исходного шага, сводка и строка зависания
	v = stepsView{PLC: 1, File: "f.pla", Sig: "Step", Signals: []sigOption{{Name: "Step", Selected: true}}}
	fillSteps(&v, j, -1, text, 0)
	fillCycles(&v, j, sum, "0", base, text)
	w := httptest.NewRecorder()
	render(w, "steps", v)
	b := strings.Join(strings.Fields(w.Body.String()), " ")
	// </html> в конце — шаблон выполнился до конца: ошибка в середине шаблона (например, обращение
	// к полю, которого нет) не роняет сервер, а молча обрывает страницу на полуслове
	for _, want := range []string{`name="home"`, `value="0" selected`, "Циклы", "<b>7 с</b>",
		"1 мин 00 с (обычно 1 с)", `<svg class="chart"`, `<polyline class="median"`, `class="long"`, "</html>"} {
		if !strings.Contains(b, want) {
			t.Errorf("страница: нет %q", want)
		}
	}
}

func TestCycleCharts(t *testing.T) {
	// 12 циклов: обычные ~60 с, к концу ~70 с, одно зависание на 5 мин (не влезает в шкалу)
	var cs []cycles.Cycle
	var tm int64
	for _, d := range []int64{60, 61, 59, 300, 60, 62, 64, 66, 69, 70, 71, 70} {
		cs = append(cs, cycles.Cycle{From: tm, To: tm + d*1000})
		tm += d*1000 + 20_000
	}
	var c cyclesView
	fillCycleCharts(&c, cs, 64_000)
	ch := c.Chart
	if len(ch.Points) != 12 || !ch.Points[3].Over || !ch.Points[3].Long || ch.Points[0].Long {
		t.Fatalf("точки: %+v", ch.Points)
	}
	if !strings.Contains(ch.TrendText, "в начале записи — 1 мин 00 с") || !strings.Contains(ch.TrendText, "+") || !ch.TrendWarn {
		t.Errorf("тренд: %q, предупреждение %v", ch.TrendText, ch.TrendWarn)
	}
	if n := len(ch.Bars); n != histBins || !ch.Bars[n-1].Rest || ch.Bars[n-1].Count != 1 {
		t.Errorf("гистограмма: %d столбцов, последний %+v", n, ch.Bars[len(ch.Bars)-1])
	}
	if len(strings.Fields(ch.Line)) != 12 {
		t.Errorf("линия медианы: %q", ch.Line)
	}
}
