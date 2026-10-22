package archive

import "testing"

func TestDownsampleKeepsPeak(t *testing.T) {
	// 100 000 точек по 100 мс: ровный ноль и один короткий пик
	const n = 100_000
	ts := make([]int64, n)
	vs := make([]float64, n)
	for i := range ts {
		ts[i] = int64(i) * 100
	}
	vs[54321] = 999

	s := Downsample(ts, vs, ts[0], ts[n-1], 500, 300)

	if len(s.T) > 2*500+10 {
		t.Errorf("точек %d — прореживание не сработало", len(s.T))
	}
	peak := 0.0
	for _, v := range s.V {
		if v != nil && *v > peak {
			peak = *v
		}
	}
	if peak != 999 {
		t.Errorf("пик потерян: максимум на графике %v", peak)
	}
}

func TestDownsampleGap(t *testing.T) {
	// 10 точек, пауза 10 с (программа не работала), ещё 10 точек
	var ts []int64
	var vs []float64
	for i := 0; i < 20; i++ {
		ms := int64(i) * 100
		if i >= 10 {
			ms += 10_000
		}
		ts = append(ts, ms)
		vs = append(vs, float64(i))
	}

	s := Downsample(ts, vs, 0, ts[len(ts)-1], 1000, 300)

	nulls := 0
	for _, v := range s.V {
		if v == nil {
			nulls++
		}
	}
	if nulls != 1 || len(s.T) != 21 {
		t.Errorf("ожидалась 20 точек и 1 разрыв, получили %d точек, разрывов %d", len(s.T), nulls)
	}
}

func TestDownsampleRange(t *testing.T) {
	ts := []int64{0, 100, 200, 300, 400, 500}
	vs := []float64{0, 1, 2, 3, 4, 5}

	s := Downsample(ts, vs, 150, 420, 100, 300) // только 200, 300, 400
	if len(s.T) != 3 || s.T[0] != 200 || *s.V[2] != 4 {
		t.Errorf("неверный интервал: %+v", s.T)
	}
}
