package buffer

import "testing"

func TestRingWrap(t *testing.T) {
	r := New(3)
	for i := int64(1); i <= 5; i++ {
		r.Add(Point{T: i})
	}

	// В буфере на 3 точки после 5 записей должны остаться 3, 4, 5
	got := r.Since(0)
	want := []int64{3, 4, 5}

	if len(got) != len(want) {
		t.Fatalf("точек %d, ожидалось %d", len(got), len(want))
	}
	for i, p := range got {
		if p.T != want[i] {
			t.Errorf("точка %d: T=%d, ожидалось %d", i, p.T, want[i])
		}
	}

	// Since(4) — только точки новее 4
	if s := r.Since(4); len(s) != 1 || s[0].T != 5 {
		t.Errorf("Since(4) = %v, ожидалось одну точку T=5", s)
	}
}
