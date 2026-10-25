// Package events — события аварий и предупреждений: фронты 0→1 («пришла») и 1→0 («ушла»)
// у битов, которые на странице «Сигналы» помечены как авария или предупреждение.
//
// Пакет ничего не знает ни о ПЛК, ни о базе: на вход — выборки, на выход — фронты.
// Поэтому его легко проверить тестами без железа.
package events

import (
	"strconv"
	"time"
)

// Bit — аварийный (или предупредительный) бит внутри выборки группы опроса
type Bit struct {
	Index   int    // номер значения в выборке группы
	Signal  string // имя сигнала
	Kind    string // alarm / warning
	Message string // текст для человека
	// Code — сигнал не бит, а целое число с номером ошибки (INT, WORD, DINT…): 0 — норма,
	// любое другое значение — авария с этим кодом. Код дописывается в сообщение.
	Code bool
}

// Edge — фронт: бит стал 1 (On) или стал 0 (!On)
type Edge struct {
	Bit Bit
	On  bool
	At  time.Time
	// Initial — «пришла» без настоящего фронта 0→1: бит был 1 уже при запуске.
	// В журнал такая авария пишется, а автоматический слепок по ней не нужен — данных «до» нет.
	Initial bool
}

// Состояние бита. unknown — ещё не видели ни одного значения (только что запустились).
const (
	unknown int8 = iota
	off
	on
)

// Detector следит за битами ОДНОЙ группы опроса.
// Не потокобезопасен: им владеет одна горутина — блок ПЛК (unit).
type Detector struct {
	bits  []Bit
	state []int8
	last  []float64 // последнее значение — у кодов ошибок, чтобы заметить смену кода
}

// NewDetector создаёт детектор.
// active — сигналы, по которым в базе уже есть НЕзакрытое событие: программу перезапустили
// (или поменяли настройки), пока авария была активна. Для них начальное состояние «1»:
//   - бит всё ещё 1 — это та же самая авария, новое событие не создаём;
//   - бит уже 0   — авария ушла, пока нас не было: закрываем событие.
func NewDetector(bits []Bit, active map[string]bool) *Detector {
	d := &Detector{bits: bits, state: make([]int8, len(bits)), last: make([]float64, len(bits))}
	for i, b := range bits {
		if active[b.Signal] {
			d.state[i] = on
		}
	}
	return d
}

// Update — пришла новая выборка группы. Возвращает фронты (обычно — ни одного).
//
//	было      стало  фронт
//	unknown   0      нет: при запуске аварии не было — сообщать не о чем
//	unknown   1      «пришла»: при запуске авария уже есть
//	0         1      «пришла»
//	1         0      «ушла»
//
// У кода ошибки «1» — любое ненулевое значение. Если код сменился, не вернувшись в 0
// (7501 → 7900), — это «ушла» старая и «пришла» новая авария, в этом порядке.
func (d *Detector) Update(at time.Time, values []float64) []Edge {
	var edges []Edge
	for i, b := range d.bits {
		if b.Index < 0 || b.Index >= len(values) {
			continue // защита от рассогласования настроек и выборки
		}
		v := values[b.Index]
		now := off
		if v != 0 { // не 0 = авария (инверсных нет)
			now = on
		}
		prev, last := d.state[i], d.last[i]
		d.state[i], d.last[i] = now, v

		switch {
		case now == on && prev != on:
			edges = append(edges, Edge{Bit: withCode(b, v), On: true, At: at, Initial: prev == unknown})
		case now == off && prev == on:
			edges = append(edges, Edge{Bit: b, On: false, At: at})
		case b.Code && now == on && last != 0 && v != last:
			// last == 0 бывает только после перезапуска во время аварии (старый код неизвестен) —
			// тогда это та же авария, ничего не делаем
			edges = append(edges,
				Edge{Bit: b, On: false, At: at},
				Edge{Bit: withCode(b, v), On: true, At: at})
		}
	}
	return edges
}

// withCode — у кода ошибки дописывает номер в сообщение: «Привод стола: код 7501»
func withCode(b Bit, v float64) Bit {
	if b.Code {
		b.Message += ": код " + strconv.FormatInt(int64(v), 10) // b — копия, исходный Bit не меняется
	}
	return b
}

// Len — сколько битов под наблюдением
func (d *Detector) Len() int { return len(d.bits) }
