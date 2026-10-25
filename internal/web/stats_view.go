package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/stats"
)

// ---------- Статистика по участку («История», слепки и Live) ----------
//
// Считается на сервере по ВСЕМ точкам: файла записи или буфера Live. Браузеру для графика
// «Истории» отдаются прореженные точки (пики сохранены), и среднее или число включений
// по ним было бы неверным.

// statRow — строка таблицы статистики: аналоговый сигнал или бит
type statRow struct {
	Name, Unit string
	Bit        bool
	Empty      bool // в участке нет ни одной точки сигнала

	// аналоговый
	Min, Max, Mean, Span string
	MinAt, MaxAt         string

	// бит
	Rises      int
	On, OnPct  string
	Longest    string
	LongestAt  string
	LongestCut bool
	Gaps       bool // данные есть не на всём участке — был разрыв связи
}

type statsView struct {
	From, To, Dur string
	Source        string // откуда точки: из файла записи или из буфера Live
	Analog, Bits  []statRow
}

// add — строка в свою таблицу: аналоговые отдельно, биты отдельно
func (v *statsView) add(row statRow) {
	if row.Bit {
		v.Bits = append(v.Bits, row)
	} else {
		v.Analog = append(v.Analog, row)
	}
}

// statRowFor — строка статистики одного сигнала на участке [from, to].
// Общая для «Истории» (точки из файла) и Live (точки из буфера).
func statRowFor(name, unit string, isBit bool, times []int64, vals []float64, from, to, periodMs int64) statRow {
	row := statRow{Name: name, Unit: unit, Bit: isBit}
	if isBit {
		// Разрыв — как на графике: пауза больше трёх периодов опроса
		b := stats.Bits(times, vals, from, to, 3*periodMs)
		row.Empty = b.N == 0
		row.Rises = b.Rises
		row.On = fmtStatDur(b.OnMs)
		if b.CoveredMs > 0 {
			row.OnPct = fmtNum(100*float64(b.OnMs)/float64(b.CoveredMs), 1) + " %"
		}
		if b.LongestMs > 0 {
			row.Longest = fmtStatDur(b.LongestMs)
			row.LongestAt = fmtStatTime(b.LongestAt)
			row.LongestCut = b.LongestCut
		}
		// Небольшую разницу на краях участка (до первой точки) разрывом не считаем
		row.Gaps = b.N > 0 && to-from-b.CoveredMs > 3*periodMs
		return row
	}

	a := stats.Analyze(times, vals, from, to)
	row.Empty = a.N == 0
	if !row.Empty {
		row.Min, row.MinAt = fmtNum(a.Min, 3), fmtStatTime(a.MinAt)
		row.Max, row.MaxAt = fmtNum(a.Max, 3), fmtStatTime(a.MaxAt)
		row.Mean = fmtNum(a.Mean, 3)
		row.Span = fmtNum(a.Max-a.Min, 3)
	}
	return row
}

// historyStats — GET /ui/history/stats?plc=…&file=…&s=…&from=…&to=… (мс)
func (s *Server) historyStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := s.openFile(r, q.Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to, ok := fileRange(f)
	if !ok {
		http.Error(w, "в файле нет данных", http.StatusNotFound)
		return
	}
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
		from = v
	}
	if v, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil {
		to = v
	}
	if to <= from {
		http.Error(w, "пустой участок", http.StatusBadRequest)
		return
	}

	v := statsView{From: fmtStatTime(from), To: fmtStatTime(to), Dur: fmtStatDur(to - from),
		Source: "по всем точкам записи"}
	for _, ref := range pickSignals(f, q["s"]) {
		gd := f.Groups[ref.g]
		isBit := strings.EqualFold(f.Header.Groups[ref.g].Signals[ref.i].Type, "BOOL")
		v.add(statRowFor(ref.Name, ref.Unit, isBit, gd.Times, gd.Values[ref.i], from, to, ref.PeriodMs))
	}
	render(w, "stats", v)
}

// liveStats — GET /ui/live/stats?plc=…&s=…&from=…&to=… (мс).
// Точки — из буфера Live этого ПЛК (последние 10 минут), а не из браузера:
// так считает тот же проверенный тестами код, что и для «Истории».
func (s *Server) liveStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	if err1 != nil || err2 != nil || to <= from {
		http.Error(w, "неверный участок", http.StatusBadRequest)
		return
	}
	want := make(map[string]bool)
	for _, n := range q["s"] {
		want[n] = true
	}

	v := statsView{From: fmtStatTime(from), To: fmtStatTime(to), Dur: fmtStatDur(to - from),
		Source: "по всем точкам буфера Live (последние 10 минут)"}
	for _, g := range s.config(s.reqPLCID(r)).Groups {
		// Какие сигналы этой группы просили
		var idx []int
		for i, n := range g.Names {
			if want[n] {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 || g.Ring == nil {
			continue
		}

		// Точки участка: время общее для группы, значения — только нужных сигналов
		times := []int64{}
		vals := make([][]float64, len(idx))
		for _, p := range g.Ring.Since(from - 1) { // Since отдаёт точки НОВЕЕ момента, поэтому from-1
			if p.T > to {
				break // точки идут от старых к новым — дальше только позже
			}
			if len(p.V) != len(g.Names) {
				continue // точка от старой конфигурации
			}
			times = append(times, p.T)
			for k, i := range idx {
				vals[k] = append(vals[k], p.V[i])
			}
		}

		for k, i := range idx {
			v.add(statRowFor(g.Names[i], at(g.Units, i), i < len(g.Bits) && g.Bits[i],
				times, vals[k], from, to, g.Period.Milliseconds()))
		}
	}
	render(w, "stats", v)
}

// at — элемент среза или "", если срез короче (защита от рассогласования)
func at(list []string, i int) string {
	if i < len(list) {
		return list[i]
	}
	return ""
}

// fmtNum — не больше prec знаков после точки, без лишних нулей: 12.5, а не 12.500.
// Точка, а не запятая — как у значений на графике.
func fmtNum(x float64, prec int) string {
	s := strconv.FormatFloat(x, 'f', prec, 64)
	if strings.Contains(s, ".") { // без точки нули не трогаем: иначе 100 стало бы 1
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" { // -0.0001 с тремя знаками
		s = "0"
	}
	return s
}

func fmtStatTime(ms int64) string { return time.UnixMilli(ms).Format("15:04:05.000") }

// fmtStatDur — длительность для человека: «850 мс», «12,3 с», «4 мин 05 с», «2 ч 10 мин»
func fmtStatDur(ms int64) string {
	switch {
	case ms < 1000:
		return strconv.FormatInt(ms, 10) + " мс"
	case ms < 60_000:
		return strings.Replace(fmtNum(float64(ms)/1000, 1), ".", ",", 1) + " с"
	case ms < 3_600_000:
		sec := ms / 1000
		return strconv.FormatInt(sec/60, 10) + " мин " + twoDigits(sec%60) + " с"
	default:
		min := ms / 60_000
		return strconv.FormatInt(min/60, 10) + " ч " + twoDigits(min%60) + " мин"
	}
}

func twoDigits(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
