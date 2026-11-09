package passport

import (
	"math"
	"testing"
)

// Среднее цикла не портится одной NaN; цикл из одних NaN — пустая ячейка
func TestAggNaN(t *testing.T) {
	a := agg([]float64{2, math.NaN(), 4})
	if !a.OK || a.Min != 2 || a.Max != 4 || a.Mean != 3 {
		t.Errorf("с NaN: %+v", a)
	}
	if a := agg([]float64{math.NaN(), math.Inf(1)}); a.OK {
		t.Errorf("одни NaN — ждали пусто: %+v", a)
	}
}
