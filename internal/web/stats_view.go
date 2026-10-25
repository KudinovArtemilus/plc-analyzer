package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/stats"
)

// ---------- Статистика по участку (страница «История» и слепки) ----------
//
// Считается на сервере по ВСЕМ точкам файла. Браузеру для графика отдаются
// прореженные точки (пики сохранены), и среднее или число включений по ним было бы неверным.

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
	Analog, Bits  []statRow
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

	v := statsView{From: fmtStatTime(from), To: fmtStatTime(to), Dur: fmtStatDur(to - from)}
	for _, ref := range pickSignals(f, q["s"]) {
		gd := f.Groups[ref.g]
		info := f.Header.Groups[ref.g].Signals[ref.i]
		times, vals := gd.Times, gd.Values[ref.i]

		row := statRow{Name: ref.Name, Unit: ref.Unit}
		if strings.EqualFold(info.Type, "BOOL") {
			row.Bit = true
			// Разрыв — как на графике: пауза больше трёх периодов опроса
			b := stats.Bits(times, vals, from, to, 3*ref.PeriodMs)
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
			row.Gaps = b.N > 0 && to-from-b.CoveredMs > 3*ref.PeriodMs
			v.Bits = append(v.Bits, row)
			continue
		}

		a := stats.Analyze(times, vals, from, to)
		row.Empty = a.N == 0
		if !row.Empty {
			row.Min, row.MinAt = fmtNum(a.Min, 3), fmtStatTime(a.MinAt)
			row.Max, row.MaxAt = fmtNum(a.Max, 3), fmtStatTime(a.MaxAt)
			row.Mean = fmtNum(a.Mean, 3)
			row.Span = fmtNum(a.Max-a.Min, 3)
		}
		v.Analog = append(v.Analog, row)
	}
	render(w, "stats", v)
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
