// Package stats — сводка по участку записи: мин / макс / среднее для аналоговых сигналов,
// число включений и время в «1» для битов.
//
// Пакет ничего не знает ни о файлах, ни о вебе: на вход — время и значения одного сигнала,
// на выход — числа. Поэтому его легко проверить тестами.
package stats

import "sort"

// Analog — сводка по аналоговому сигналу (INT, REAL…) на участке
type Analog struct {
	N              int     // сколько точек попало в участок; 0 — данных нет
	Min, Max, Mean float64 // Mean — среднее по точкам
	MinAt, MaxAt   int64   // когда был минимум и максимум (мс Unix), первый раз
}

// Bit — сводка по биту (BOOL) на участке
type Bit struct {
	N          int   // сколько точек попало в участок; 0 — данных нет
	Rises      int   // включений: переходов 0→1
	OnMs       int64 // сколько времени бит был в «1»
	CoveredMs  int64 // сколько времени вообще есть данные (без разрывов связи)
	LongestMs  int64 // самое долгое непрерывное «1»
	LongestAt  int64 // когда оно началось (мс Unix)
	LongestCut bool  // оно упирается в край участка — на самом деле могло быть длиннее
}

// span — индексы точек участка [from, to]: t[lo:hi].
// t отсортирован по возрастанию, поэтому ищем двоичным поиском, а не перебором.
func span(t []int64, from, to int64) (lo, hi int) {
	lo = sort.Search(len(t), func(i int) bool { return t[i] >= from })
	hi = sort.Search(len(t), func(i int) bool { return t[i] > to })
	return lo, hi
}

// Analyze — сводка по аналоговому сигналу.
//
// Среднее — простое среднее по точкам. Это честно, потому что сигнал группы опрашивается
// с постоянным периодом: каждая точка «весит» одинаковое время. Разрывы связи
// в среднее не попадают — в них просто нет точек.
func Analyze(t []int64, v []float64, from, to int64) Analog {
	lo, hi := span(t, from, to)
	var a Analog
	if lo >= hi {
		return a
	}
	sum := 0.0
	a.Min, a.Max = v[lo], v[lo]
	a.MinAt, a.MaxAt = t[lo], t[lo]
	for i := lo; i < hi; i++ {
		x := v[i]
		sum += x
		if x < a.Min { // строгое «<»: при равных значениях запоминаем ПЕРВЫЙ раз
			a.Min, a.MinAt = x, t[i]
		}
		if x > a.Max {
			a.Max, a.MaxAt = x, t[i]
		}
	}
	a.N = hi - lo
	a.Mean = sum / float64(a.N)
	return a
}

// Bits — сводка по биту.
//
// Значение держится от своей точки до следующей (как ступенька на графике), но не дольше
// gapMs: пауза больше gapMs — разрыв связи, и что было с битом в это время, мы не знаем.
// Последняя точка держится до конца участка (to), тоже не дольше gapMs.
func Bits(t []int64, v []float64, from, to, gapMs int64) Bit {
	lo, hi := span(t, from, to)
	var b Bit
	if lo >= hi {
		return b
	}
	b.N = hi - lo

	runStart := int64(-1) // начало текущего «1»; -1 — сейчас не в «1»
	runCut := false       // текущее «1» началось с первой точки участка — до неё могло быть тоже «1»
	closeRun := func(end int64, cut bool) {
		if runStart < 0 {
			return
		}
		if d := end - runStart; d > b.LongestMs {
			b.LongestMs, b.LongestAt, b.LongestCut = d, runStart, runCut || cut
		}
		runStart = -1
	}

	for i := lo; i < hi; i++ {
		on := v[i] != 0

		// До какого момента держится эта точка
		end := to
		if i+1 < hi {
			end = t[i+1]
		}
		gap := end-t[i] > gapMs
		if gap {
			end = t[i] + gapMs
		}
		d := end - t[i]
		b.CoveredMs += d

		if on {
			b.OnMs += d
			if i > lo && v[i-1] == 0 {
				b.Rises++ // первую точку участка не считаем: что было до неё — не видно
			}
			if runStart < 0 {
				runStart, runCut = t[i], i == lo
			}
		} else {
			closeRun(t[i], false)
		}
		// Разрыв связи обрывает «1»: дальше мы не знаем, продолжалось ли оно
		if on && gap {
			closeRun(end, i+1 == hi) // разрыв в самом конце участка — «упирается в край»
		}
	}
	closeRun(to, true) // «1» держится до конца участка
	return b
}
