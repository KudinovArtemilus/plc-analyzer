package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/cycles"
)

// ---------- Графики циклов (32c): время цикла по ходу смены и гистограмма ----------
//
// Рисуем SVG прямо на сервере: координаты считает Go, шаблон только расставляет
// <circle> и <rect>. Ни JavaScript, ни библиотек графиков — страница остаётся простой,
// а картинку можно сохранить вместе со страницей.

const (
	chartW, chartH = 760, 170 // область графика, px
	chartL, chartB = 64, 22   // поля: слева под подписи оси Y, снизу под подписи оси X
	histBins       = 16
	rollWindow     = 9 // окно скользящей медианы, циклов
)

type chartPoint struct {
	X, Y string
	Long bool   // дольше 1,5 × обычного — выделить
	Over bool   // не влез в шкалу (дольше 3 × обычного): нарисован у верхнего края
	Tip  string // подсказка при наведении
}

type chartTick struct {
	Pos    string // координата
	Label  string
	Anchor string // выравнивание подписи: start / middle / end — крайние не вылезают за рамку
}

type chartBar struct {
	X, Y, W, H string
	TextX      string // где подписать число циклов
	Count      int
	Tip        string
	Rest       bool // «и дольше»
}

type cyclesChart struct {
	W, H, L, B   int // размеры и поля — для шаблона
	PlotW, PlotH int
	Points       []chartPoint
	Line         string // скользящая медиана: «x,y x,y …» для <polyline>
	XTicks       []chartTick
	YTicks       []chartTick

	Bars      []chartBar
	BarsTicks []chartTick // подписи под гистограммой
	TrendText string
	TrendWarn bool // цикл вырос больше чем на 5 %
}

// f — координата для SVG: одна цифра после точки, точка (не запятая — это не текст для человека)
func f(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }

// fillCycleCharts — оба графика по циклам (в порядке времени) и подпись тренда
func fillCycleCharts(c *cyclesView, cs []cycles.Cycle, medianMs int64) {
	if len(cs) < 2 {
		return
	}
	ch := &c.Chart
	ch.W, ch.H, ch.L, ch.B = chartW+chartL+10, chartH+chartB+8, chartL, chartB
	ch.PlotW, ch.PlotH = chartW, chartH

	// Шкала Y: от 0 до 3 × обычного (или до самого долгого, если он короче)
	top := 3 * medianMs
	maxMs := int64(0)
	for _, cy := range cs {
		maxMs = max(maxMs, cy.Dur())
	}
	top = min(top, maxMs)
	top = max(top, medianMs*5/4) // запас сверху: когда все циклы одинаковые, линия не прилипает к рамке
	if top <= 0 {
		return
	}
	t0, t1 := cs[0].From, cs[len(cs)-1].From
	if t1 == t0 {
		t1 = t0 + 1
	}
	x := func(t int64) float64 { return float64(chartL) + float64(t-t0)/float64(t1-t0)*chartW }
	y := func(ms int64) float64 { return 4 + chartH - float64(min(ms, top))/float64(top)*chartH }

	for _, cy := range cs {
		d := cy.Dur()
		ch.Points = append(ch.Points, chartPoint{
			X: f(x(cy.From)), Y: f(y(d)),
			Long: d*2 > medianMs*3, Over: d > top,
			Tip: fmtStatTime(cy.From) + " — " + fmtStatDur(d),
		})
	}
	var line []string
	for i, m := range cycles.RollingMedian(cs, rollWindow) {
		line = append(line, f(x(cs[i].From))+","+f(y(m)))
	}
	ch.Line = strings.Join(line, " ")

	ch.YTicks = []chartTick{
		{Pos: f(y(0)), Label: "0"},
		{Pos: f(y(medianMs)), Label: fmtStatDur(medianMs)},
		{Pos: f(y(top)), Label: fmtStatDur(top)},
	}
	clock := func(ms int64) string { return time.UnixMilli(ms).Format("15:04") }
	ch.XTicks = []chartTick{
		{Pos: f(x(t0)), Label: clock(t0), Anchor: "start"},
		{Pos: f(x((t0 + t1) / 2)), Label: clock((t0 + t1) / 2), Anchor: "middle"},
		{Pos: f(x(t1)), Label: clock(t1), Anchor: "end"},
	}

	// Гистограмма: та же ширина, столбцы по числу циклов
	bins := cycles.Histogram(cs, histBins)
	most := 1
	for _, b := range bins {
		most = max(most, b.Count)
	}
	bw := float64(chartW) / float64(len(bins))
	for i, b := range bins {
		h := float64(b.Count) / float64(most) * (chartH - 14) // сверху место под число над столбцом
		bar := chartBar{
			X: f(float64(chartL) + float64(i)*bw + 1), W: f(bw - 2),
			Y: f(4 + chartH - h), H: f(h),
			TextX: f(float64(chartL) + (float64(i)+0.5)*bw),
			Count: b.Count, Rest: b.Rest,
			Tip: fmtStatDur(b.FromMs) + " – " + fmtStatDur(b.ToMs) + ": " + strconv.Itoa(b.Count),
		}
		if b.Rest {
			bar.Tip = "дольше " + fmtStatDur(b.FromMs) + ": " + strconv.Itoa(b.Count)
		}
		ch.Bars = append(ch.Bars, bar)
	}
	if len(bins) > 0 {
		first, last := bins[0], bins[len(bins)-1]
		ch.BarsTicks = []chartTick{
			{Pos: f(float64(chartL)), Label: fmtStatDur(first.FromMs), Anchor: "start"},
			{Pos: f(float64(chartL + chartW)), Label: fmtStatDur(last.ToMs), Anchor: "end"},
		}
		if last.Rest {
			ch.BarsTicks[1].Label = "дольше " + fmtStatDur(last.FromMs)
		}
	}

	// Тренд: обычный цикл в начале и в конце записи
	if tr := cycles.TrendOf(cs); tr.OK && tr.FirstMs > 0 {
		diff := tr.LastMs - tr.FirstMs
		ch.TrendText = "Обычный цикл в начале записи — " + fmtStatDur(tr.FirstMs) +
			", в конце — " + fmtStatDur(tr.LastMs)
		switch {
		case max(diff, -diff)*100 < tr.FirstMs: // меньше 1 % — это не тренд, а шум
			ch.TrendText += " — не изменился"
		case diff > 0:
			ch.TrendText += " (+" + fmtStatDur(diff) + ", +" + pct(diff, tr.FirstMs) + ")"
		default:
			ch.TrendText += " (−" + fmtStatDur(-diff) + ", −" + pct(-diff, tr.FirstMs) + ")"
		}
		ch.TrendWarn = diff*20 > tr.FirstMs // вырос больше чем на 5 %
	}
}
