package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// ---------- Страница «Шаги»: журнал шаговой цепочки по файлу смены ----------
//
// GET /steps?plc=&file=&sig=&min=
//   file — файл смены (как в «Истории»), sig — сигнал номера шага,
//   min  — показывать в журнале проходы дольше min секунд; пусто — «необычно долгие».
//
// Считаем по ВСЕМ точкам файла, а не по прореженным для графика: короткий шаг
// на 200 мс между точками обзора потерялся бы.

// unusual — проход «необычно долгий», если он дольше медианы своего шага в столько раз…
const unusual = 3

// …и не короче этого: шаг, который обычно длится 100 мс, на 400 мс — не повод смотреть
const unusualMinMs = 1000

// journalMax — больше строк журнала не показываем: за смену переходов бывают десятки тысяч
const journalMax = 500

type stepsView struct {
	PLC     int64
	File    string
	Title   string      // ПЛК · дата · смена
	Signals []sigOption // целые сигналы файла — кандидаты в номер шага
	Sig     string      // выбранный
	Min     string      // фильтр журнала, как ввели
	Err     string

	From, To, Dur string // запись
	Passes        int    // всего проходов (строк журнала)
	Summary       []stepStatView
	Journal       []stepView
	JournalNote   string // чем отфильтрован журнал и не обрезан ли он
}

// sigOption — строка выбора сигнала. «Выбран» считаем в Go: в шаблоне только {{if .Selected}}
// (урок 30a — чем меньше логики в шаблоне, тем меньше её может испортить форматтер).
type sigOption struct {
	Name     string
	Selected bool
}

type stepStatView struct {
	Value        int64
	Text         string // из справочника
	Count, Full  int
	Partial      bool // есть обрезанные проходы — показать, сколько полных
	Total, Pct   string
	Median, Mean string // по полным проходам; пусто — полных нет
	Max, MaxAt   string
	MaxCut       bool
}

type stepView struct {
	Value     int64
	Text      string
	From, Dur string
	Long      bool // необычно долгий — выделить
	CutStart  bool
	CutEnd    bool
}

// stepsPage — GET /steps
func (s *Server) stepsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v := stepsView{PLC: s.reqPLCID(r), File: q.Get("file"), Sig: q.Get("sig"), Min: strings.TrimSpace(q.Get("min"))}

	f, err := s.openFile(r, v.File)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h := f.Header
	v.Title = h.PLC + " · " + h.ShiftDate + " · смена " + h.Shift

	// Номер шага — целое число: BOOL и REAL не подходят
	for _, g := range h.Groups {
		for _, sg := range g.Signals {
			if t := strings.ToUpper(sg.Type); t != "BOOL" && t != "REAL" {
				v.Signals = append(v.Signals, sigOption{Name: sg.Name, Selected: sg.Name == v.Sig})
			}
		}
	}
	if v.Sig == "" {
		render(w, "steps", v) // сначала выбрать сигнал
		return
	}

	refs := pickSignals(f, []string{v.Sig})
	if len(refs) != 1 {
		v.Err = "в этой записи нет сигнала «" + v.Sig + "»"
		render(w, "steps", v)
		return
	}
	ref := refs[0]
	gd := f.Groups[ref.g]

	minMs := int64(-1) // -1 — «необычно долгие»
	if v.Min != "" {
		sec, err := strconv.ParseFloat(strings.Replace(v.Min, ",", ".", 1), 64)
		if err != nil || sec < 0 {
			v.Err = "«дольше»: число секунд, например 10 или 2,5"
		} else {
			minMs = int64(sec * 1000)
		}
	}

	l := s.lookup(r.Context(), v.PLC)
	text := func(n int64) string { t, _ := l.text(v.Sig, n); return t }

	// Разрыв — как на графике и в статистике: пауза больше трёх периодов опроса
	fillSteps(&v, steps.Journal(gd.Times, gd.Values[ref.i], 3*ref.PeriodMs), minMs, text)
	render(w, "steps", v)
}

// fillSteps — журнал и сводка в вид для страницы. Отдельно от обработчика,
// чтобы проверять тестом без файлов: на вход — готовый журнал.
func fillSteps(v *stepsView, j []steps.Step, minMs int64, text func(int64) string) {
	if len(j) == 0 {
		v.Err = "в записи нет точек этого сигнала"
		return
	}
	from, to := j[0].From, j[len(j)-1].To
	v.From, v.To, v.Dur = fmtStatTime(from), fmtStatTime(to), fmtStatDur(to-from)
	v.Passes = len(j)

	sum := steps.Summary(j)
	median := make(map[int64]int64, len(sum)) // для «необычно долгих»
	for _, st := range sum {
		median[st.Value] = st.MedianMs
		row := stepStatView{
			Value: st.Value, Text: text(st.Value), Count: st.Count, Full: st.Full, Partial: st.Full < st.Count,
			Total: fmtStatDur(st.TotalMs),
			Max:   fmtStatDur(st.MaxMs), MaxAt: fmtStatTime(st.MaxAt), MaxCut: st.MaxCut,
		}
		if to > from {
			row.Pct = strings.Replace(fmtNum(100*float64(st.TotalMs)/float64(to-from), 1), ".", ",", 1) + " %"
		}
		if st.Full > 0 {
			row.Median, row.Mean = fmtStatDur(st.MedianMs), fmtStatDur(st.MeanMs)
		}
		v.Summary = append(v.Summary, row)
	}

	// long — необычно долгий: в unusual раз дольше медианы своего шага и не короче unusualMinMs.
	// Обрезанные проходы сравнивать не с чем — их длительность неполная.
	long := func(st steps.Step) bool {
		m := median[st.Value]
		return st.Full() && m > 0 && st.Dur() >= unusualMinMs && st.Dur() > unusual*m
	}
	show := long
	v.JournalNote = "необычно долгие проходы: в " + strconv.Itoa(unusual) +
		" раза дольше обычного (медианы) своего шага и не короче " + fmtStatDur(unusualMinMs)
	if minMs >= 0 {
		show = func(st steps.Step) bool { return st.Dur() >= minMs }
		v.JournalNote = "проходы дольше " + fmtStatDur(minMs)
		if minMs == 0 {
			v.JournalNote = "все проходы"
		}
	}

	shown := 0
	for _, st := range j {
		if !show(st) {
			continue
		}
		shown++
		if len(v.Journal) < journalMax {
			v.Journal = append(v.Journal, stepView{
				Value: st.Value, Text: text(st.Value),
				From: fmtStatTime(st.From), Dur: fmtStatDur(st.Dur()),
				Long: long(st), CutStart: st.CutStart, CutEnd: st.CutEnd,
			})
		}
	}
	v.JournalNote += " — " + strconv.Itoa(shown) + " из " + strconv.Itoa(len(j))
	if shown > journalMax {
		v.JournalNote += "; показаны первые " + strconv.Itoa(journalMax) + " — уточните фильтр"
	}
}
