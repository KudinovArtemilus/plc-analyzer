// Package events — события аварий и предупреждений: фронты 0→1 («пришла») и 1→0 («ушла»)
// у битов, которые на странице «Сигналы» помечены как авария или предупреждение.
//
// Пакет ничего не знает ни о ПЛК, ни о базе: на вход — выборки, на выход — фронты.
// Поэтому его легко проверить тестами без железа.
package events

import "time"

// Bit — аварийный (или предупредительный) бит внутри выборки группы опроса
type Bit struct {
	Index   int    // номер значения в выборке группы
	Signal  string // имя сигнала
	Kind    string // alarm / warning
	Message string // текст для человека
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
}

// NewDetector создаёт детектор.
// active — сигналы, по которым в базе уже есть НЕзакрытое событие: программу перезапустили
// (или поменяли настройки), пока авария была активна. Для них начальное состояние «1»:
//   - бит всё ещё 1 — это та же самая авария, новое событие не создаём;
//   - бит уже 0   — авария ушла, пока нас не было: закрываем событие.
func NewDetector(bits []Bit, active map[string]bool) *Detector {
	d := &Detector{bits: bits, state: make([]int8, len(bits))}
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
func (d *Detector) Update(at time.Time, values []float64) []Edge {
	var edges []Edge
	for i, b := range d.bits {
		if b.Index < 0 || b.Index >= len(values) {
			continue // защита от рассогласования настроек и выборки
		}
		now := off
		if values[b.Index] != 0 { // 1 = авария (инверсных нет)
			now = on
		}
		prev := d.state[i]
		d.state[i] = now

		switch {
		case now == on && prev != on:
			edges = append(edges, Edge{Bit: b, On: true, At: at, Initial: prev == unknown})
		case now == off && prev == on:
			edges = append(edges, Edge{Bit: b, On: false, At: at})
		}
	}
	return edges
}

// Len — сколько битов под наблюдением
func (d *Detector) Len() int { return len(d.bits) }
