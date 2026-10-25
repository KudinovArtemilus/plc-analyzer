package events

import (
	"math"
	"strconv"
	"time"
)

// ---------- Нормы аналоговых сигналов ----------
//
// Выход за норму превращается в обычный фронт Edge — как у аварийного бита. Поэтому дальше
// всё готовое работает без изменений: журнал, панель аварий, шунты и слепки по аварии.
// У каждой нормы своё «имя события»: имя сигнала + суффикс, например "P_hyd#hi".

// Суффиксы имён событий для норм. Решётка — разделитель, которого нет в обычных именах сигналов.
const (
	SufHi   = "#hi"
	SufLo   = "#lo"
	SufJump = "#jump"
)

// Limit — нормы одного аналогового сигнала внутри выборки группы опроса
type Limit struct {
	Index  int    // номер значения в выборке группы
	Signal string // имя сигнала
	Text   string // как назвать сигнал в сообщении: его сообщение или имя
	Unit   string
	Kind   string // alarm / warning
	Lo, Hi *float64
	Hyst   float64
	Jump   *float64
	JumpMs int64
}

// EventNames — имена событий, которые может породить эта норма
// (нужны, чтобы не закрыть их в журнале как «снятые с контроля»)
func (l Limit) EventNames() []string {
	var out []string
	if l.Hi != nil {
		out = append(out, l.Signal+SufHi)
	}
	if l.Lo != nil {
		out = append(out, l.Signal+SufLo)
	}
	if l.Jump != nil {
		out = append(out, l.Signal+SufJump)
	}
	return out
}

// sample — точка истории для поиска скачка
type sample struct {
	t int64 // мс
	v float64
}

type limState struct {
	hi, lo, jump int8     // unknown / off / on — как у битов
	hist         []sample // последние JumpMs миллисекунд (только если следим за скачком)
}

// LimitDetector следит за нормами сигналов ОДНОЙ группы опроса.
// Не потокобезопасен: им владеет горутина блока ПЛК (unit), как и Detector.
type LimitDetector struct {
	lims  []Limit
	state []limState
}

// NewLimitDetector создаёт детектор. active — события, открытые в базе (как у NewDetector):
// для них начальное состояние «нарушено», чтобы после перезапуска не плодить дубли.
func NewLimitDetector(lims []Limit, active map[string]bool) *LimitDetector {
	d := &LimitDetector{lims: lims, state: make([]limState, len(lims))}
	for i, l := range lims {
		if active[l.Signal+SufHi] {
			d.state[i].hi = on
		}
		if active[l.Signal+SufLo] {
			d.state[i].lo = on
		}
		if active[l.Signal+SufJump] {
			d.state[i].jump = on
		}
	}
	return d
}

// Update — пришла новая выборка группы. Возвращает фронты нарушений норм.
func (d *LimitDetector) Update(at time.Time, values []float64) []Edge {
	var edges []Edge
	ms := at.UnixMilli()
	for i, l := range d.lims {
		if l.Index < 0 || l.Index >= len(values) {
			continue
		}
		v := values[l.Index]
		st := &d.state[i] // указатель: меняем состояние в срезе, а не его копию

		// Верхняя граница: нарушена, когда выше Hi; восстановилась — когда не выше Hi−Hyst
		if l.Hi != nil {
			if e, ok := level(&st.hi, v > *l.Hi, v <= *l.Hi-l.Hyst); ok {
				edges = append(edges, d.edge(l, SufHi, e, at,
					"выше "+num(*l.Hi)+unit(l.Unit)))
			}
		}
		// Нижняя граница — зеркально
		if l.Lo != nil {
			if e, ok := level(&st.lo, v < *l.Lo, v >= *l.Lo+l.Hyst); ok {
				edges = append(edges, d.edge(l, SufLo, e, at,
					"ниже "+num(*l.Lo)+unit(l.Unit)))
			}
		}
		// Скачок: |v − значение JumpMs назад| > Jump
		if l.Jump != nil {
			st.hist = append(st.hist, sample{ms, v})
			// Выбрасываем точки старше окна. Срез «съезжает» вперёд; append сам
			// перевыделит память, когда старое начало станет недоступно — утечки нет.
			k := 0
			for k < len(st.hist)-1 && st.hist[k].t < ms-l.JumpMs {
				k++
			}
			st.hist = st.hist[k:]
			ref := st.hist[0].v // самая старая точка в окне
			jumped := math.Abs(v-ref) > *l.Jump
			// Начальное состояние скачка — «нет»: для него нужна история, при запуске её нет
			if st.jump == unknown {
				st.jump = off
			}
			if e, ok := level(&st.jump, jumped, !jumped); ok {
				edges = append(edges, d.edge(l, SufJump, e, at,
					"скачок больше "+num(*l.Jump)+unit(l.Unit)+" за "+strconv.FormatInt(l.JumpMs, 10)+" мс"))
			}
		}
	}
	return edges
}

// edgeKind — что случилось с нормой
type edgeKind int8

const (
	cameInitial edgeKind = iota // нарушена уже при запуске
	came                        // нарушилась
	went                        // восстановилась
)

// level — общий автомат «нарушено / в норме» с зоной возврата.
// bad — условие нарушения, good — условие восстановления (между ними — «как было»).
func level(state *int8, bad, good bool) (edgeKind, bool) {
	prev := *state
	switch {
	case bad && prev != on:
		*state = on
		if prev == unknown {
			return cameInitial, true
		}
		return came, true
	case good && prev == on:
		*state = off
		return went, true
	case !bad && prev == unknown:
		*state = off // при запуске в норме (или в зоне возврата) — сообщать не о чем
	}
	return 0, false
}

func (d *LimitDetector) edge(l Limit, suf string, k edgeKind, at time.Time, what string) Edge {
	return Edge{
		Bit:     Bit{Index: l.Index, Signal: l.Signal + suf, Kind: l.Kind, Message: l.Text + ": " + what},
		On:      k != went,
		At:      at,
		Initial: k == cameInitial,
	}
}

// Len — сколько сигналов с нормами под наблюдением
func (d *LimitDetector) Len() int { return len(d.lims) }

func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

func unit(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}
