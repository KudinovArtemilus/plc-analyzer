// Package cycles — циклы работы (обычно одна деталь) по журналу шаговой цепочки.
//
// Цикл — путь от выхода из исходного шага до возврата в него: цепочка ушла из «Базовой
// позиции», прошла свои шаги и вернулась. Время в самом исходном шаге — ожидание между
// циклами (простой), в длительность цикла оно не входит.
//
// Как и steps, пакет ничего не знает ни о файлах, ни о вебе: на вход — журнал шагов,
// на выход — циклы. Поэтому его легко проверить тестами.
package cycles

import (
	"sort"

	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
)

// Cycle — один цикл
type Cycle struct {
	From, To int64 // вышли из исходного шага → вернулись в него (мс Unix)
	Passes   int   // сколько проходов по шагам внутри цикла

	// Ожидание в исходном шаге перед этим циклом. IdleCut — ожидание видно не целиком
	// (началось до начала записи или после обрыва связи / смены режима).
	IdleMs  int64
	IdleCut bool

	// Шаг, который в этом цикле затянулся сильнее всего относительно своей медианы:
	// SlowStep стоял SlowMs вместо обычных SlowMedianMs. SlowMs == 0 — сравнивать не с чем.
	SlowStep     int64
	SlowMs       int64
	SlowMedianMs int64
	SlowAt       int64 // когда вошли в этот шаг
}

// Dur — длительность цикла, мс
func (c Cycle) Dur() int64 { return c.To - c.From }

// Split — нарезать журнал шагов на циклы. home — номер исходного шага.
//
// Цикл берётся, только если виден целиком: выход из исходного шага, все шаги внутри
// и возврат в него — без обрывов связи и без смены режима (обрезанных проходов).
// Остальные считаются неполными (incomplete): про них известно, что цикл был, но не сколько он длился.
func Split(j []steps.Step, home int64) (cs []Cycle, incomplete int) {
	// Медианы шагов — чтобы найти в каждом цикле самый затянувшийся шаг
	median := map[int64]int64{}
	for _, st := range steps.Summary(j) {
		median[st.Value] = st.MedianMs
	}

	prev := -1 // индекс последнего прохода исходного шага
	for k, s := range j {
		if s.Value != home {
			continue
		}
		if prev >= 0 && k > prev+1 { // между двумя проходами исходного шага есть шаги — это цикл
			if c, ok := cycle(j[prev], j[prev+1:k], s, median); ok {
				cs = append(cs, c)
			} else {
				incomplete++
			}
		}
		prev = k
	}
	return cs, incomplete
}

// cycle — цикл между проходами исходного шага h1 и h2; ok=false — виден не целиком
func cycle(h1 steps.Step, inside []steps.Step, h2 steps.Step, median map[int64]int64) (Cycle, bool) {
	if h1.CutEnd || h2.CutStart {
		return Cycle{}, false // выход из исходного шага или возврат в него не виден
	}
	c := Cycle{
		From: inside[0].From, To: h2.From, Passes: len(inside),
		IdleMs: h1.Dur(), IdleCut: h1.CutStart,
	}
	worst := 0.0
	for _, s := range inside {
		if !s.Full() {
			return Cycle{}, false // обрыв связи или смена режима внутри цикла
		}
		m := median[s.Value]
		if m <= 0 {
			continue
		}
		if r := float64(s.Dur()) / float64(m); r > worst {
			worst = r
			c.SlowStep, c.SlowMs, c.SlowMedianMs, c.SlowAt = s.Value, s.Dur(), m, s.From
		}
	}
	return c, true
}

// Stat — сводка по циклам
type Stat struct {
	Count      int // полных циклов
	Incomplete int // неполных: обрыв связи, смена режима, край записи внутри цикла

	MedianMs, MeanMs, MinMs, MaxMs int64 // время цикла

	WorkMs int64 // сумма времени циклов — станок работал
	IdleMs int64 // сумма ожидания в исходном шаге перед полными циклами — стоял между деталями
}

// Summarize — сводка по циклам. Медиана — «обычное» время цикла: одна задержка её не сдвигает.
func Summarize(cs []Cycle, incomplete int) Stat {
	st := Stat{Count: len(cs), Incomplete: incomplete}
	if len(cs) == 0 {
		return st
	}
	ds := make([]int64, len(cs))
	st.MinMs = cs[0].Dur()
	for i, c := range cs {
		d := c.Dur()
		ds[i] = d
		st.WorkMs += d
		st.IdleMs += c.IdleMs
		st.MinMs, st.MaxMs = min(st.MinMs, d), max(st.MaxMs, d)
	}
	st.MeanMs = st.WorkMs / int64(len(cs))
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	if m := len(ds) / 2; len(ds)%2 == 1 {
		st.MedianMs = ds[m]
	} else {
		st.MedianMs = (ds[m-1] + ds[m]) / 2
	}
	return st
}

// Longest — n самых долгих циклов, от самого долгого
func Longest(cs []Cycle, n int) []Cycle {
	out := append([]Cycle(nil), cs...) // копия: сортировка не должна менять порядок циклов у вызывающего
	sort.SliceStable(out, func(a, b int) bool { return out[a].Dur() > out[b].Dur() })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// SuggestHome — кандидат в исходный шаг: тот, в котором цепочка провела больше всего
// времени и в который возвращалась хотя бы дважды (иначе цикла между возвратами нет).
// Сводка steps.Summary уже отсортирована по суммарному времени. ok=false — кандидата нет.
func SuggestHome(sum []steps.Stat) (home int64, ok bool) {
	for _, st := range sum {
		if st.Count >= 2 {
			return st.Value, true
		}
	}
	return 0, false
}
