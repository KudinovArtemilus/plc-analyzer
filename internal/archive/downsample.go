package archive

import (
	"math"
	"sort"
)

// Finite — обычное число, не NaN и не бесконечность. REAL из ПЛК бывает NaN («не число»:
// 0/0 в SCL, корень из минуса, не тот адрес или тип), а JSON NaN передать не умеет —
// поэтому для графика, статистики и CSV такие точки — «нет значения», как разрыв связи.
// В файле .pla они остаются как есть: это правда о том, что пришло из ПЛК.
func Finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// Series — точки одного сигнала для графика. nil в V — разрыв линии (нет связи или «не число»).
type Series struct {
	T []int64    `json:"t"`
	V []*float64 `json:"v"`
}

// Downsample готовит точки сигнала на интервале [from, to] для графика шириной buckets.
// Если точек мало — отдаёт как есть. Если много — по каждому отрезку минимум и максимум,
// так ни один пик не пропадает. Пауза больше gapMs между соседними точками — разрыв.
func Downsample(t []int64, v []float64, from, to int64, buckets int, gapMs int64) Series {
	lo := sort.Search(len(t), func(i int) bool { return t[i] >= from })
	hi := sort.Search(len(t), func(i int) bool { return t[i] > to })
	t, v = t[lo:hi], v[lo:hi]

	var out Series
	add := func(ti int64, vi float64) {
		out.T = append(out.T, ti)
		if !Finite(vi) {
			out.V = append(out.V, nil) // «не число» — разрыв линии в этой точке
			return
		}
		x := vi // своя переменная на каждую точку: указатели не должны смотреть в одно место
		out.V = append(out.V, &x)
	}
	gap := func(after int64) {
		out.T = append(out.T, after+1)
		out.V = append(out.V, nil)
	}

	// Мало точек — без прореживания
	if len(t) <= 2*buckets {
		for i := range t {
			if i > 0 && t[i]-t[i-1] > gapMs {
				gap(t[i-1])
			}
			add(t[i], v[i])
		}
		return out
	}

	width := float64(to-from) / float64(buckets)
	for i := 0; i < len(t); {
		b := int(float64(t[i]-from) / width)
		if b >= buckets {
			b = buckets - 1
		}
		end := from + int64(float64(b+1)*width)

		// Собираем точки отрезка; разрыв внутри — останавливаемся на нём.
		// Минимум и максимум — только среди обычных чисел: с NaN любое сравнение ложно,
		// и одна такая точка в начале отрезка «спрятала» бы настоящий пик.
		mi, ma, j := -1, -1, i
		// Первую точку отрезка (j == i) берём всегда — иначе на последней точке записи,
		// где t == to == end, цикл не сдвинулся бы с места
		for j < len(t) && (j == i || t[j] < end && t[j]-t[j-1] <= gapMs) {
			if Finite(v[j]) {
				if mi < 0 || v[j] < v[mi] {
					mi = j
				}
				if ma < 0 || v[j] > v[ma] {
					ma = j
				}
			}
			j++
		}

		if mi < 0 { // в отрезке одни «не числа» — разрыв
			add(t[i], math.NaN())
		} else {
			// Минимум и максимум — в том порядке, в каком они были по времени
			first, second := min(mi, ma), max(mi, ma)
			add(t[first], v[first])
			if second != first {
				add(t[second], v[second])
			}
		}

		if j < len(t) && t[j]-t[j-1] > gapMs {
			gap(t[j-1])
		}
		i = j
	}
	return out
}

// CountIn — сколько точек попадает в интервал [from, to]
func CountIn(t []int64, from, to int64) int {
	lo := sort.Search(len(t), func(i int) bool { return t[i] >= from })
	hi := sort.Search(len(t), func(i int) bool { return t[i] > to })
	return hi - lo
}
