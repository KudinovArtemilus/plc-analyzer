// Package summary — короткая сводка для телефона (шаг 45a-4): что сейчас со станком
// и как прошла смена в процентах.
//
// Здесь только то, что уйдёт наружу (типы JSON), и расчёты. Ни файлов, ни базы, ни веба:
// на вход — уже прочитанные отрезки времени, на выход — числа. Собирает входные данные
// пакет web (tg_summary.go). Поэтому всё легко проверить тестами.
//
// ПРАВИЛО: в типах ниже нет и не должно быть полей для IP, адресов сигналов (DB…),
// путей к файлам и настроек. Что не описано здесь — наружу уйти не может.
package summary

import (
	"math"
	"slices"
	"sort"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// ---------- Что уходит наружу ----------

// Summary — вся сводка
type Summary struct {
	At     string `json:"at"`     // когда собрана, RFC 3339 с часовым поясом
	Period int    `json:"period"` // как часто будет приходить, с: телефон по нему поймёт «давно не было»
	Areas  []Area `json:"areas"`
}

// Area — участок и его станки (только те, у которых стоит «показывать»)
type Area struct {
	Name     string    `json:"name"`
	Machines []Machine `json:"machines"`
}

// Machine — карточка станка. Пустые поля не уходят: что не отмечено в админке, того нет в JSON.
type Machine struct {
	Name    string   `json:"name"`
	Online  bool     `json:"online"`
	State   *State   `json:"state,omitempty"`
	Step    string   `json:"step,omitempty"` // «12 · Зажим закрыт»
	Shift   *Shift   `json:"shift,omitempty"`
	Signals []Signal `json:"signals,omitempty"`
	Alarms  []Alarm  `json:"alarms,omitempty"`
	Log     []Logged `json:"log,omitempty"` // аварии за смену, новые сверху (для карточки станка, 45c)
}

// Logged — авария за смену: когда пришла, текст, сколько длилась (идёт — до «сейчас»)
type Logged struct {
	At     string `json:"at"` // «09:14»
	Text   string `json:"text"`
	Min    int    `json:"min"`
	Active bool   `json:"active,omitempty"`
}

// LogMax — сколько аварий за смену класть в сводку: она едет в ссылке «Табло» (до 8000 символов)
const LogMax = 10

// State — состояние сейчас: название и цвет (ключ из store.TGColors, red — авария, gray — нет связи)
type State struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Shift — смена: с какого часа и доли времени
type Shift struct {
	Name   string  `json:"name"` // «А» / «Б»
	From   string  `json:"from"` // «08:00»
	Min    int     `json:"min"`  // сколько минут смены прошло
	Shares []Share `json:"shares"`
}

// Share — доля смены в одном состоянии: проценты и минуты
type Share struct {
	Name  string  `json:"name"`
	Color string  `json:"color"`
	Pct   float64 `json:"pct"`
	Min   int     `json:"min"`
}

// Signal — сигнал карточки. Value/Min/Max — указатели: нет значения — нет и поля (а не 0).
type Signal struct {
	Label string   `json:"label"`
	Value *float64 `json:"value,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Text  string   `json:"text,omitempty"` // «с подписью из справочника»
}

// Alarm — активная авария: текст и с какого времени
type Alarm struct {
	Text  string `json:"text"`
	Since string `json:"since"` // «11:36»
}

// Цвета встроенных состояний
const (
	AlarmColor   = "red"
	OfflineColor = "gray"
)

// ---------- Состояния ----------

// Rule — состояние из админки: по коду (Codes — значения сигнала режима) или по биту (Bit — имя BOOL)
type Rule struct {
	Name  string
	Color string
	Pct   bool // показывать долю в полоске смены
	Codes []int64
	Bit   string
}

// Rules — правила станка: свои состояния по порядку и включены ли «Авария» и «Нет связи»
type Rules struct {
	States  []Rule
	Alarm   bool
	Offline bool
}

// Moment — что известно в один момент
type Moment struct {
	Online  bool
	Alarm   bool            // есть активная авария
	Mode    int64           // значение сигнала режима
	HasMode bool            // значение режима известно
	Bits    map[string]bool // биты состояний «по биту»; нет в карте — неизвестно (= не 1)
}

// Что получилось у Classify, кроме номера своего состояния
const (
	InNone    = -1 // ни одно состояние не подошло (или «Нет связи» выключено в админке)
	InAlarm   = -2
	InOffline = -3
)

// Classify — какое состояние у станка в этот момент. Порядок важности:
// нет связи → авария → свои состояния сверху вниз (первое подошедшее).
// Выключенные в админке «Авария» / «Нет связи» не участвуют: без связи и без «Нет связи» — InNone.
func Classify(r Rules, m Moment) int {
	if !m.Online {
		if r.Offline {
			return InOffline
		}
		return InNone
	}
	if r.Alarm && m.Alarm {
		return InAlarm
	}
	for i, st := range r.States {
		if st.Bit != "" {
			if m.Bits[st.Bit] {
				return i
			}
			continue
		}
		if m.HasMode && slices.Contains(st.Codes, m.Mode) {
			return i
		}
	}
	return InNone
}

// StateOf — состояние для карточки (nil — ни одно не подошло)
func StateOf(r Rules, k int) *State {
	switch {
	case k == InAlarm:
		return &State{Name: "Авария", Color: AlarmColor}
	case k == InOffline:
		return &State{Name: "Нет связи", Color: OfflineColor}
	case k >= 0 && k < len(r.States):
		return &State{Name: r.States[k].Name, Color: r.States[k].Color}
	}
	return nil
}

// ---------- Смена в процентах ----------

// Timeline — всё о смене, уже разложенное по отрезкам времени (мс Unix)
type Timeline struct {
	From, To int64                   // от начала смены до «сейчас»
	Online   []steps.Span            // когда была запись (вне их — «нет связи»: не знаем, что было)
	Alarms   []steps.Span            // когда была активна хоть одна авария (могут перекрываться)
	Mode     []steps.Step            // журнал сигнала режима (steps.Journal)
	Bits     map[string][]steps.Step // журналы битов состояний «по биту»
}

// Shares — доли смены по состояниям. В каждый момент станок ровно в одном состоянии
// (Classify), поэтому доли не перекрываются и в сумме не больше 100 %. Остаток до 100 —
// время, когда не подошло ни одно состояние (или его доля не показывается: Pct выключен).
//
// Как считаем: собираем все моменты, где хоть что-то меняется (граница записи, аварии,
// смена режима или бита), и между соседними моментами всё постоянно — классифицируем
// начало отрезка и прибавляем его длину. Отрезков — тысячи за смену, это быстро.
func Shares(r Rules, tl Timeline) []Share {
	base := tl.To - tl.From
	if base <= 0 {
		return nil
	}
	cuts := []int64{tl.From, tl.To}
	addSpans := func(sp []steps.Span) {
		for _, s := range sp {
			cuts = append(cuts, s.From, s.To)
		}
	}
	addSteps := func(j []steps.Step) {
		for _, s := range j {
			cuts = append(cuts, s.From, s.To)
		}
	}
	addSpans(tl.Online)
	addSpans(tl.Alarms)
	addSteps(tl.Mode)
	for _, j := range tl.Bits {
		addSteps(j)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)

	online, alarms := merge(tl.Online), merge(tl.Alarms)
	ms := map[int]int64{}
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if a < tl.From || b > tl.To {
			continue // граница записи или аварии вне смены
		}
		m := Moment{Online: inSpans(online, a), Alarm: inSpans(alarms, a), Bits: map[string]bool{}}
		m.Mode, m.HasMode = valueAt(tl.Mode, a)
		for name, j := range tl.Bits {
			v, ok := valueAt(j, a)
			m.Bits[name] = ok && v == 1
		}
		ms[Classify(r, m)] += b - a
	}

	var out []Share
	add := func(name, color string, d int64) {
		out = append(out, Share{Name: name, Color: color,
			Pct: math.Round(float64(d)*1000/float64(base)) / 10, Min: int(math.Round(float64(d) / 60000))})
	}
	for i, st := range r.States {
		if st.Pct {
			add(st.Name, st.Color, ms[i])
		}
	}
	if r.Alarm {
		add("Авария", AlarmColor, ms[InAlarm])
	}
	if r.Offline {
		add("Нет связи", OfflineColor, ms[InOffline])
	}
	return out
}

// valueAt — значение журнала в момент t: отрезок, в котором From <= t < To.
// Журнал идёт по времени, поэтому ищем двоичным поиском.
func valueAt(j []steps.Step, t int64) (int64, bool) {
	i := sort.Search(len(j), func(i int) bool { return j[i].To > t })
	if i < len(j) && j[i].From <= t {
		return j[i].Value, true
	}
	return 0, false
}

// inSpans — попадает ли t в один из отрезков (отрезки — по времени, без перекрытий)
func inSpans(sp []steps.Span, t int64) bool {
	i := sort.Search(len(sp), func(i int) bool { return sp[i].To > t })
	return i < len(sp) && sp[i].From <= t
}

// merge — объединить перекрывающиеся отрезки: две аварии сразу — это всё равно одна «Авария»
func merge(sp []steps.Span) []steps.Span {
	sp = slices.Clone(sp)
	sort.Slice(sp, func(a, b int) bool { return sp[a].From < sp[b].From })
	var out []steps.Span
	for _, s := range sp {
		if s.To <= s.From {
			continue
		}
		if n := len(out); n > 0 && s.From <= out[n-1].To {
			out[n-1].To = max(out[n-1].To, s.To)
			continue
		}
		out = append(out, s)
	}
	return out
}

// Round — значение для телефона: не больше 2 знаков после запятой (182.39999 → 182.4)
func Round(x float64) float64 { return math.Round(x*100) / 100 }
