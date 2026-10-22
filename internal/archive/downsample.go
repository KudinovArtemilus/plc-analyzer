package archive

import "sort"

// Series — точки одного сигнала для графика. nil в V — разрыв линии.
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
		x := vi // своя переменная на каждую точку: указатели не должны смотреть в одно место
		out.T = append(out.T, ti)
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

		// Собираем точки отрезка; разрыв внутри — останавливаемся на нём
		mi, ma, j := i, i, i+1
		for j < len(t) && t[j] < end && t[j]-t[j-1] <= gapMs {
			if v[j] < v[mi] {
				mi = j
			}
			if v[j] > v[ma] {
				ma = j
			}
			j++
		}

		// Минимум и максимум — в том порядке, в каком они были по времени
		first, second := min(mi, ma), max(mi, ma)
		add(t[first], v[first])
		if second != first {
			add(t[second], v[second])
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
