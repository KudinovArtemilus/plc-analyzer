package archive

import (
	"math"
	"testing"
)

// «Не число» из ПЛК: на графике — разрыв, соседние точки и пики не теряются
func TestDownsampleNaN(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)

	// Мало точек — без прореживания: NaN и Inf — nil на своём месте
	s := Downsample([]int64{0, 100, 200, 300}, []float64{1, nan, inf, 4}, 0, 300, 100, 1000)
	if len(s.V) != 4 || s.V[0] == nil || *s.V[0] != 1 || s.V[1] != nil || s.V[2] != nil || *s.V[3] != 4 {
		t.Errorf("без прореживания: %v", vals(s))
	}

	// Много точек: NaN первым в отрезке не должен «спрятать» настоящие мин и макс
	var tt []int64
	var vv []float64
	for i := range 1000 {
		tt = append(tt, int64(i)*100)
		x := float64(i % 10)
		if i%100 == 0 {
			x = nan
		}
		vv = append(vv, x)
	}
	vv[555] = 99 // пик
	s = Downsample(tt, vv, 0, tt[len(tt)-1], 10, 1000)
	peak := false
	for _, p := range s.V {
		if p != nil && math.IsNaN(*p) {
			t.Fatal("NaN попал в точки графика")
		}
		if p != nil && *p == 99 {
			peak = true
		}
	}
	if !peak {
		t.Error("пик 99 пропал")
	}

	// Отрезок из одних «не чисел» — разрыв
	all := make([]float64, 1000)
	for i := range all {
		all[i] = nan
	}
	s = Downsample(tt, all, 0, tt[len(tt)-1], 10, 1000)
	for _, p := range s.V {
		if p != nil {
			t.Fatal("в отрезке одни NaN — ждали только разрывы")
		}
	}
}

func vals(s Series) []any {
	var out []any
	for _, p := range s.V {
		if p == nil {
			out = append(out, nil)
		} else {
			out = append(out, *p)
		}
	}
	return out
}
