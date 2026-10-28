package web

import (
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/cycles"
	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// ---------- Раздел «Циклы» на странице «Шаги» ----------
//
// Цикл — от выхода из исходного шага до возврата в него (пакет cycles).
// Исходный шаг выбирается на странице; не выбран — программа предлагает сама (SuggestHome).
// Считается по тому же журналу, что и сводка «по шагам»: если выбран режим — только в нём.

// longestMax — сколько самых долгих циклов показать
const longestMax = 10

type cyclesView struct {
	Home     []valOption // выбор исходного шага: все шаги записи
	HomeAuto bool        // исходный шаг предложен программой (по подписи или по времени), а не выбран
	HomeName string      // «0 · Базовая позиция»

	Count, Incomplete            int
	Median, Mean, MinDur, MaxDur string
	Work, WorkPct, Idle, IdlePct string
	Longest                      []cycleView
	Note                         string      // почему циклов нет
	Chart                        cyclesChart // время цикла по ходу смены и гистограмма
}

type cycleView struct {
	From, Dur, Idle string
	Passes          int
	Slow            string // «12 · Зажим закрыт: 5 с (обычно 1 с)»
	Long            bool   // в 1,5 раза дольше обычного цикла — выделить
	Link            string
}

// fillCycles — раздел «Циклы». j — журнал (уже обрезанный по режиму, если он выбран),
// sum — его сводка, home — номер исходного шага из адреса («» — предложить),
// baseMs — от чего считать долю (время в режиме или вся запись).
func fillCycles(v *stepsView, j []steps.Step, sum []steps.Stat, home string, baseMs int64, text func(int64) string) {
	c := &v.Cycles
	h, err := strconv.ParseInt(home, 10, 64)
	if err != nil {
		var ok bool
		if h, ok = homeByText(sum, text); !ok {
			h, ok = cycles.SuggestHome(sum)
		}
		if !ok {
			c.Note = "цепочка ни разу не вернулась в один и тот же шаг дважды — циклов не видно"
			return
		}
		c.HomeAuto = true
	}
	for _, st := range sum {
		c.Home = append(c.Home, valOption{Value: st.Value, Text: text(st.Value), Selected: st.Value == h})
	}
	sortValOptions(c.Home)
	c.HomeName = withText(h, text(h))

	cs, inc := cycles.Split(j, h)
	st := cycles.Summarize(cs, inc)
	c.Count, c.Incomplete = st.Count, st.Incomplete
	if st.Count == 0 {
		c.Note = "полных циклов нет: цепочка не прошла путь от «" + c.HomeName + "» и обратно целиком"
		return
	}
	c.Median, c.Mean = fmtStatDur(st.MedianMs), fmtStatDur(st.MeanMs)
	c.MinDur, c.MaxDur = fmtStatDur(st.MinMs), fmtStatDur(st.MaxMs)
	c.Work, c.Idle = fmtStatDur(st.WorkMs), fmtStatDur(st.IdleMs)
	if baseMs > 0 {
		c.WorkPct, c.IdlePct = pct(st.WorkMs, baseMs), pct(st.IdleMs, baseMs)
	}

	fillCycleCharts(c, cs, st.MedianMs)

	for _, cy := range cycles.Longest(cs, longestMax) {
		row := cycleView{
			From: fmtStatTime(cy.From), Dur: fmtStatDur(cy.Dur()), Passes: cy.Passes,
			Idle: fmtStatDur(cy.IdleMs),
			Long: cy.Dur()*2 > st.MedianMs*3, // > 1,5 × обычного — в целых числах, без float
			Link: stepLink(v, steps.Step{From: cy.From, To: cy.To}),
		}
		if cy.IdleCut {
			row.Idle = "≥ " + row.Idle
		}
		// «Затянулся» — только если шаг правда стоял заметно дольше обычного:
		// в 1,5 раза и хотя бы на секунду. Иначе это просто самый «тяжёлый» из нормальных шагов.
		if cy.SlowMs > 0 && cy.SlowMs*2 >= cy.SlowMedianMs*3 && cy.SlowMs-cy.SlowMedianMs >= 1000 {
			row.Slow = withText(cy.SlowStep, text(cy.SlowStep)) + ": " + fmtStatDur(cy.SlowMs) +
				" (обычно " + fmtStatDur(cy.SlowMedianMs) + ")"
		}
		c.Longest = append(c.Longest, row)
	}
}

// homeWords — как в справочниках шагов обычно называют исходное положение
var homeWords = []string{"исходн", "базов", "ожидан", "home", "init", "idle"}

// homeByText — исходный шаг по подписи из справочника («0 · Базовая позиция»):
// подпись знает человек, и она надёжнее догадки по времени. Сводка отсортирована по времени,
// поэтому из нескольких подходящих берётся тот, где цепочка стоит дольше.
func homeByText(sum []steps.Stat, text func(int64) string) (int64, bool) {
	for _, st := range sum {
		t := strings.ToLower(text(st.Value))
		for _, w := range homeWords {
			if st.Count >= 2 && strings.Contains(t, w) {
				return st.Value, true
			}
		}
	}
	return 0, false
}

// withText — «12 · Зажим закрыт» или просто «12»
func withText(n int64, t string) string {
	s := strconv.FormatInt(n, 10)
	if t != "" {
		s += " · " + t
	}
	return s
}

// pct — доля part от whole: «58,8 %»
func pct(part, whole int64) string {
	return strings.Replace(fmtNum(100*float64(part)/float64(whole), 1), ".", ",", 1) + " %"
}
