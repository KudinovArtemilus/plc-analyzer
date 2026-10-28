// Package passport — «паспорт здоровья» циклов: по каждому полному циклу смены считаются
// признаки, которые меняются при износе. Строка на цикл:
//
//   - время цикла, ожидание перед ним и время каждого шага — ось стала ездить медленнее,
//     значит дольше стоит её шаг;
//   - min / max / среднее каждого числового сигнала внутри цикла — давление не добирает,
//     масло греется, ось не доезжает до прежнего положения;
//   - сколько раз за цикл включался каждый бит — дребезг датчика, повторные попытки, аварии.
//
// Сырые записи живут 30 дней, а паспорт маленький (сотни КБ в месяц) и хранится всегда:
// по нему видно тренды за месяцы — это основа предиктивного анализа (Python, 34b/34c).
//
// Как steps и cycles, пакет не знает ни о вебе, ни о базе: на вход — файл смены и настройка,
// на выход — таблица. Поэтому его легко проверить тестами.
package passport

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/cycles"
	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// Config — как найти циклы в записи ПЛК. Те же три выбора, что на странице «Шаги».
type Config struct {
	Step    string // сигнал номера шага
	Mode    string // сигнал режима работы; "" — режим не учитывать
	ModeVal int64  // значение режима, в котором считать циклы (обычно «автомат»)
	Home    int64  // исходный шаг — граница цикла
}

// Agg — числовой сигнал внутри одного цикла
type Agg struct {
	Min, Max, Mean float64
	OK             bool // в цикле были точки этого сигнала (у медленной группы их может не быть)
}

// Row — один цикл
type Row struct {
	cycles.Cycle
	StepMs map[int64]int64 // сколько цепочка простояла в каждом шаге за цикл (шаг мог быть дважды)
	Num    map[string]Agg  // числовые сигналы
	Ons    map[string]int  // включения 0→1 битов
}

// Table — паспорт одной записи
type Table struct {
	Steps      []int64  // все шаги, встретившиеся в циклах, по возрастанию — столбцы step_N_s
	Num        []string // числовые сигналы в порядке файла
	Bits       []string // биты в порядке файла
	Rows       []Row
	Incomplete int // неполных циклов (обрыв связи, смена режима) — в таблицу не попали
}

// ErrNoSignal — в записи нет нужного сигнала (переименовали или убрали из опроса)
var ErrNoSignal = errors.New("в записи нет сигнала")

// sig — где сигнал лежит в файле
type sig struct {
	g, i  int
	name  string
	bit   bool
	gapMs int64 // разрыв связи — пауза больше трёх периодов опроса, как на графике и в «Шагах»
}

func find(f *archive.File, name string) (sig, bool) {
	for gi, g := range f.Header.Groups {
		for si, s := range g.Signals {
			if s.Name == name {
				return sig{g: gi, i: si, name: name, bit: strings.EqualFold(s.Type, "BOOL"), gapMs: 3 * g.PeriodMs}, true
			}
		}
	}
	return sig{}, false
}

// Build — паспорт записи: циклы и их признаки
func Build(f *archive.File, c Config) (Table, error) {
	var t Table
	st, ok := find(f, c.Step)
	if !ok {
		return t, fmt.Errorf("%w шага «%s»", ErrNoSignal, c.Step)
	}
	sd := f.Groups[st.g]
	j := steps.Journal(sd.Times, sd.Values[st.i], st.gapMs)

	// Только время в выбранном режиме — как на странице «Шаги» (31d)
	var md sig
	if c.Mode != "" {
		if md, ok = find(f, c.Mode); !ok {
			return t, fmt.Errorf("%w режима «%s»", ErrNoSignal, c.Mode)
		}
		mg := f.Groups[md.g]
		mj := steps.Journal(mg.Times, mg.Values[md.i], md.gapMs)
		j = steps.Clip(j, steps.Spans(mj, c.ModeVal))
	}

	cs, inc := cycles.Split(j, c.Home)
	t.Incomplete = inc

	// Какие сигналы описывать: все, кроме самих шага и режима
	var sigs []sig
	for gi, g := range f.Header.Groups {
		for si, s := range g.Signals {
			if s.Name == c.Step || (c.Mode != "" && s.Name == c.Mode) {
				continue
			}
			x := sig{g: gi, i: si, name: s.Name, bit: strings.EqualFold(s.Type, "BOOL")}
			sigs = append(sigs, x)
			if x.bit {
				t.Bits = append(t.Bits, x.name)
			} else {
				t.Num = append(t.Num, x.name)
			}
		}
	}

	seen := map[int64]bool{}
	k := 0 // проходы журнала идут по времени, циклы тоже — указатель только вперёд
	for _, cy := range cs {
		r := Row{Cycle: cy, StepMs: map[int64]int64{}, Num: map[string]Agg{}, Ons: map[string]int{}}

		// Время каждого шага: проходы, начавшиеся внутри цикла
		for k < len(j) && j[k].From < cy.From {
			k++
		}
		for p := k; p < len(j) && j[p].From < cy.To; p++ {
			r.StepMs[j[p].Value] += j[p].Dur()
			seen[j[p].Value] = true
		}

		for _, s := range sigs {
			gd := f.Groups[s.g]
			lo, hi := window(gd.Times, cy.From, cy.To)
			v := gd.Values[s.i]
			if s.bit {
				r.Ons[s.name] = ons(v, lo, hi)
			} else if lo < hi {
				r.Num[s.name] = agg(v[lo:hi])
			}
		}
		t.Rows = append(t.Rows, r)
	}

	for v := range seen {
		t.Steps = append(t.Steps, v)
	}
	sort.Slice(t.Steps, func(a, b int) bool { return t.Steps[a] < t.Steps[b] })
	return t, nil
}

// window — индексы точек со временем в [from, to). Время в файле растёт — ищем делением пополам.
func window(t []int64, from, to int64) (lo, hi int) {
	lo = sort.Search(len(t), func(i int) bool { return t[i] >= from })
	hi = sort.Search(len(t), func(i int) bool { return t[i] >= to })
	return lo, hi
}

// ons — включения 0→1 в точках [lo, hi). Точку lo сравниваем с предыдущей:
// бит, включившийся ровно в момент начала цикла, тоже считается.
func ons(v []float64, lo, hi int) int {
	n := 0
	for i := max(lo, 1); i < hi; i++ {
		if v[i-1] == 0 && v[i] != 0 {
			n++
		}
	}
	return n
}

func agg(v []float64) Agg {
	a := Agg{Min: v[0], Max: v[0], OK: true}
	sum := 0.0
	for _, x := range v {
		a.Min, a.Max = min(a.Min, x), max(a.Max, x)
		sum += x
	}
	a.Mean = sum / float64(len(v))
	return a
}
