// Package steps — журнал шаговой цепочки: в какой шаг перешла, когда и сколько в нём стояла,
// и сводка «по шагам» — где цепочка проводит время.
//
// Как и stats, пакет ничего не знает ни о файлах, ни о вебе: на вход — время и значения
// одного сигнала (номер шага), на выход — шаги. Поэтому его легко проверить тестами.
package steps

import (
	"math"
	"sort"
)

// Step — один проход цепочки через шаг
type Step struct {
	Value    int64 // номер шага
	From, To int64 // вошли и вышли (мс Unix)

	// Обрезан: длительность неполная, на самом деле шаг мог длиться дольше.
	// CutStart — шаг уже шёл, когда началась запись (или связь вернулась после обрыва).
	// CutEnd   — запись кончилась или связь оборвалась, а шаг ещё шёл.
	CutStart, CutEnd bool
}

// Dur — длительность прохода, мс
func (s Step) Dur() int64 { return s.To - s.From }

// Full — проход виден целиком: и вход, и выход
func (s Step) Full() bool { return !s.CutStart && !s.CutEnd }

// Journal — переходы по точкам сигнала: где номер сменился — там новый шаг.
//
// Номер держится от своей точки до следующей. Пауза между точками больше gapMs — обрыв
// связи: что было в это время, мы не знаем, поэтому шаг до обрыва закрывается (CutEnd),
// а после обрыва начинается заново (CutStart), даже если номер тот же.
//
// «Дребезг» (12 → 13 → 12 за 100 мс) не фильтруем: это настоящие данные ПЛК.
func Journal(t []int64, v []float64, gapMs int64) []Step {
	var out []Step
	if len(t) == 0 {
		return out
	}
	cur := Step{Value: num(v[0]), From: t[0], CutStart: true} // что было до записи — не видно

	for i := 1; i < len(t); i++ {
		if t[i]-t[i-1] > gapMs {
			// Обрыв связи: последнее, что знаем, — шаг шёл в момент t[i-1]
			cur.To, cur.CutEnd = t[i-1], true
			out = append(out, cur)
			cur = Step{Value: num(v[i]), From: t[i], CutStart: true}
			continue
		}
		if n := num(v[i]); n != cur.Value {
			cur.To = t[i] // переход виден в этой точке — с точностью до периода опроса
			out = append(out, cur)
			cur = Step{Value: n, From: t[i]}
		}
	}
	cur.To, cur.CutEnd = t[len(t)-1], true // запись кончилась, а шаг ещё шёл
	return append(out, cur)
}

// num — номер шага из значения сигнала. Значения записаны как float64 (так хранится
// любой сигнал); у целого сигнала с масштабом 1 это и есть целое, Round — от погрешности.
func num(x float64) int64 { return int64(math.Round(x)) }

// Stat — сводка по одному номеру шага
type Stat struct {
	Value   int64
	Count   int   // сколько раз цепочка проходила шаг (и обрезанные проходы тоже)
	Full    int   // из них полных — по ним считаются среднее и медиана
	TotalMs int64 // сколько времени цепочка провела в шаге за всю запись (все проходы)

	MeanMs, MedianMs int64 // по полным проходам; 0, если полных нет

	MaxMs  int64 // самый долгий проход (среди всех)
	MaxAt  int64 // когда он начался (мс Unix)
	MaxCut bool  // он обрезан — на самом деле мог быть ещё дольше («≥»)
}

// Summary — сводка по шагам. Сначала шаги, где цепочка провела больше всего времени:
// это и есть ответ на вопрос «где теряется время».
//
// Медиана важнее среднего: одно зависание на 5 минут сильно сдвинет среднее,
// а медиана покажет, сколько шаг длится обычно.
func Summary(j []Step) []Stat {
	byValue := map[int64]*Stat{}
	full := map[int64][]int64{} // длительности полных проходов — для медианы
	var order []int64           // номера в порядке первого появления (map порядок не хранит)

	for _, s := range j {
		st := byValue[s.Value]
		if st == nil {
			st = &Stat{Value: s.Value}
			byValue[s.Value] = st
			order = append(order, s.Value)
		}
		d := s.Dur()
		st.Count++
		st.TotalMs += d
		if d > st.MaxMs || st.Count == 1 {
			st.MaxMs, st.MaxAt, st.MaxCut = d, s.From, !s.Full()
		}
		if s.Full() {
			st.Full++
			full[s.Value] = append(full[s.Value], d)
		}
	}

	out := make([]Stat, 0, len(order))
	for _, n := range order {
		st := byValue[n]
		if ds := full[n]; len(ds) > 0 {
			var sum int64
			for _, d := range ds {
				sum += d
			}
			st.MeanMs = sum / int64(len(ds))
			st.MedianMs = median(ds)
		}
		out = append(out, *st)
	}
	// Больше всего времени — наверху; при равенстве — по номеру шага, чтобы порядок был всегда один
	sort.Slice(out, func(a, b int) bool {
		if out[a].TotalMs != out[b].TotalMs {
			return out[a].TotalMs > out[b].TotalMs
		}
		return out[a].Value < out[b].Value
	})
	return out
}

// median — медиана; при чётном числе — среднее двух средних. Портит порядок в ds (сортирует).
func median(ds []int64) int64 {
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	m := len(ds) / 2
	if len(ds)%2 == 1 {
		return ds[m]
	}
	return (ds[m-1] + ds[m]) / 2
}

// ---------- Только в режиме: отрезки времени и обрезка журнала по ним ----------

// Span — отрезок времени [From, To], мс
type Span struct{ From, To int64 }

// Spans — отрезки, когда сигнал был равен v. Журнал сигнала режима строится тем же Journal:
// режим — такой же номер, как шаг («1 · Автомат», «2 · Ручной»).
func Spans(j []Step, v int64) []Span {
	var out []Span
	for _, s := range j {
		if s.Value == v && s.To > s.From {
			out = append(out, Span{s.From, s.To})
		}
	}
	return out
}

// Clip — оставить от журнала только то, что попадает в отрезки.
//
// Проход, который начался или кончился вне отрезка, обрезается по его границе и помечается
// CutStart / CutEnd с этой стороны: в выбранном режиме он виден не целиком, и в медиану
// не пойдёт. Шаг, который шёл через смену режима, даст две части — обе обрезанные.
//
// И журнал, и отрезки идут по времени, поэтому обходим их вместе двумя указателями:
// без вложенного перебора «каждый с каждым» — за смену тут десятки тысяч проходов.
func Clip(j []Step, spans []Span) []Step {
	var out []Step
	k := 0 // первый отрезок, который ещё может пересечься с текущим проходом
	for _, s := range j {
		for k < len(spans) && spans[k].To <= s.From {
			k++ // отрезок кончился до начала прохода — дальше он никому не нужен
		}
		for i := k; i < len(spans) && spans[i].From < s.To; i++ {
			from, to := max(s.From, spans[i].From), min(s.To, spans[i].To)
			if to <= from {
				continue
			}
			out = append(out, Step{
				Value: s.Value, From: from, To: to,
				CutStart: s.CutStart || from > s.From, // начало срезано границей режима
				CutEnd:   s.CutEnd || to < s.To,
			})
		}
	}
	return out
}

// Total — суммарная длина отрезков, мс
func Total(spans []Span) int64 {
	var t int64
	for _, sp := range spans {
		t += sp.To - sp.From
	}
	return t
}
