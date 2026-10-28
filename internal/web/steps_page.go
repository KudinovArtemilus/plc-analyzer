package web

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// ---------- Страница «Шаги»: журнал шаговой цепочки по файлу смены ----------
//
// GET /steps?plc=&file=&sig=&min=&mode=&mv=&home=
//   file — файл смены (как в «Истории»), sig — сигнал номера шага,
//   min  — показывать в журнале проходы дольше min секунд; пусто — «необычно долгие»,
//   mode — сигнал режима работы, mv — его значение: считать только время в этом режиме,
//   home — исходный шаг для циклов; пусто — программа предложит сама.
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

	// Режим работы: показать, сколько времени в каждом режиме, и считать шаги только в одном
	ModeSigs []sigOption    // кандидаты в сигнал режима (те же целые сигналы)
	ModeVals []valOption    // значения режима, встретившиеся в записи
	Modes    []stepStatView // время по режимам за запись
	ModeNote string         // «только в режиме «Автомат»: 6 ч 20 мин из 12 ч»
	Mode     string         // выбранный сигнал режима — его тоже показать на графике
	ModeVal  string         // выбранное значение режима (как в адресе)

	Cycles cyclesView // раздел «Циклы»

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

// valOption — значение режима в выборе: число, текст из справочника, выбрано ли
type valOption struct {
	Value    int64
	Text     string
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
	Link      string // этот проход на графике
}

// stepPad — запас по краям участка на графике: доля длительности прохода, но не меньше 5 с,
// чтобы было видно, что было ДО входа в шаг и ПОСЛЕ выхода
const stepPadMin = 5000

// stepLink — просмотр записи на участке прохода: курсор A — вход в шаг, B — выход,
// Δt между ними — длительность. На графике — сигнал шага и (если выбран) сигнал режима;
// набор сигналов можно переключить уже там, участок сохранится.
func stepLink(v *stepsView, st steps.Step) string {
	pad := max(stepPadMin, st.Dur()/10)
	q := url.Values{}
	q.Set("plc", strconv.FormatInt(v.PLC, 10))
	q.Set("file", v.File)
	q.Add("s", v.Sig)
	if v.Mode != "" && v.Mode != v.Sig {
		q.Add("s", v.Mode)
	}
	q.Set("from", strconv.FormatInt(st.From-pad, 10))
	q.Set("to", strconv.FormatInt(st.To+pad, 10))
	q.Set("ca", strconv.FormatInt(st.From, 10))
	q.Set("cb", strconv.FormatInt(st.To, 10))
	return "/view.html?" + q.Encode()
}

// stepsPage — GET /steps
func (s *Server) stepsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v := stepsView{PLC: s.reqPLCID(r), File: q.Get("file"), Sig: q.Get("sig"), Min: strings.TrimSpace(q.Get("min"))}
	mode, mv := q.Get("mode"), q.Get("mv")
	v.Mode, v.ModeVal = mode, mv

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
				v.ModeSigs = append(v.ModeSigs, sigOption{Name: sg.Name, Selected: sg.Name == mode})
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
	j := steps.Journal(gd.Times, gd.Values[ref.i], 3*ref.PeriodMs)

	// Режим работы: время по режимам и, если выбрано значение, — только это время
	var baseMs int64 // от чего считать «долю»; 0 — от всей записи
	if mode != "" {
		if mr := pickSignals(f, []string{mode}); len(mr) == 1 {
			md := f.Groups[mr[0].g]
			modeText := func(n int64) string { t, _ := l.text(mode, n); return t }
			mj := steps.Journal(md.Times, md.Values[mr[0].i], 3*mr[0].PeriodMs)
			j, baseMs = fillModes(&v, mj, mv, modeText, j)
		} else {
			v.Err = "в этой записи нет сигнала режима «" + mode + "»"
		}
	}
	fillSteps(&v, j, minMs, text, baseMs)
	if len(j) > 0 {
		if baseMs == 0 {
			baseMs = j[len(j)-1].To - j[0].From
		}
		fillCycles(&v, j, steps.Summary(j), q.Get("home"), baseMs, text)
	}
	render(w, "steps", v)
}

// fillModes — таблица «время по режимам» и выбор значения режима. Если значение выбрано,
// возвращает журнал шагов, обрезанный по времени в этом режиме, и длительность режима
// (для «доли»); иначе — журнал как есть и 0.
func fillModes(v *stepsView, mj []steps.Step, mv string, text func(int64) string, j []steps.Step) ([]steps.Step, int64) {
	if len(mj) == 0 {
		return j, 0
	}
	sum := steps.Summary(mj)
	v.Modes = statRows(sum, mj[len(mj)-1].To-mj[0].From, text)

	want, err := strconv.ParseInt(mv, 10, 64)
	selected := err == nil
	for _, st := range sum {
		v.ModeVals = append(v.ModeVals, valOption{Value: st.Value, Text: text(st.Value),
			Selected: selected && st.Value == want})
	}
	sortValOptions(v.ModeVals)
	if !selected {
		return j, 0
	}

	spans := steps.Spans(mj, want)
	in := steps.Total(spans)
	name := mv
	if t := text(want); t != "" {
		name = mv + " · " + t
	}
	v.ModeNote = "только в режиме «" + name + "»: " + fmtStatDur(in) + " из " + fmtStatDur(mj[len(mj)-1].To-mj[0].From)
	return steps.Clip(j, spans), in
}

// sortValOptions — значения в выборе по возрастанию номера
func sortValOptions(vs []valOption) {
	sort.Slice(vs, func(a, b int) bool { return vs[a].Value < vs[b].Value })
}

// statRows — сводка в строки таблицы; base — от чего считать долю, мс
func statRows(sum []steps.Stat, base int64, text func(int64) string) []stepStatView {
	rows := make([]stepStatView, 0, len(sum))
	for _, st := range sum {
		row := stepStatView{
			Value: st.Value, Text: text(st.Value), Count: st.Count, Full: st.Full, Partial: st.Full < st.Count,
			Total: fmtStatDur(st.TotalMs),
			Max:   fmtStatDur(st.MaxMs), MaxAt: fmtStatTime(st.MaxAt), MaxCut: st.MaxCut,
		}
		if base > 0 {
			row.Pct = strings.Replace(fmtNum(100*float64(st.TotalMs)/float64(base), 1), ".", ",", 1) + " %"
		}
		if st.Full > 0 {
			row.Median, row.Mean = fmtStatDur(st.MedianMs), fmtStatDur(st.MeanMs)
		}
		rows = append(rows, row)
	}
	return rows
}

// fillSteps — журнал и сводка в вид для страницы. Отдельно от обработчика,
// чтобы проверять тестом без файлов: на вход — готовый журнал.
// baseMs — от чего считать «долю»: время в выбранном режиме; 0 — вся запись.
func fillSteps(v *stepsView, j []steps.Step, minMs int64, text func(int64) string, baseMs int64) {
	if len(j) == 0 {
		if v.ModeNote != "" {
			v.Err = "в выбранном режиме цепочка не работала"
		} else {
			v.Err = "в записи нет точек этого сигнала"
		}
		return
	}
	from, to := j[0].From, j[len(j)-1].To
	v.From, v.To, v.Dur = fmtStatTime(from), fmtStatTime(to), fmtStatDur(to-from)
	v.Passes = len(j)
	if baseMs == 0 {
		baseMs = to - from
	}

	sum := steps.Summary(j)
	median := make(map[int64]int64, len(sum)) // для «необычно долгих»
	for _, st := range sum {
		median[st.Value] = st.MedianMs
	}
	v.Summary = statRows(sum, baseMs, text)

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
				Link: stepLink(v, st),
			})
		}
	}
	v.JournalNote += " — " + strconv.Itoa(shown) + " из " + strconv.Itoa(len(j))
	if shown > journalMax {
		v.JournalNote += "; показаны первые " + strconv.Itoa(journalMax) + " — уточните фильтр"
	}
}
