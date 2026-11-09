package stats

import (
	"math"
	"testing"
)

// «Не числа» в мин/макс/среднее не входят, но считаются
func TestAnalyzeNaN(t *testing.T) {
	nan := math.NaN()
	a := Analyze([]int64{0, 1, 2, 3, 4}, []float64{nan, 5, math.Inf(-1), 1, 3}, 0, 4)
	if a.N != 3 || a.Bad != 2 || a.Min != 1 || a.Max != 5 || a.Mean != 3 || a.MinAt != 3 || a.MaxAt != 1 {
		t.Errorf("с NaN: %+v", a)
	}
	a = Analyze([]int64{0, 1}, []float64{nan, nan}, 0, 1)
	if a.N != 0 || a.Bad != 2 {
		t.Errorf("одни NaN: %+v", a)
	}
}
