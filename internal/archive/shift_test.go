package archive

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(day, hour, min int) time.Time {
	return time.Date(2026, 10, day, hour, min, 0, 0, time.Local)
}

func TestShiftOf(t *testing.T) {
	tests := []struct {
		t        time.Time
		wantDay  int
		wantName string
	}{
		{at(21, 8, 0), 21, "A"},   // ровно начало дневной
		{at(21, 14, 30), 21, "A"},
		{at(21, 19, 59), 21, "A"}, // последняя минута дневной
		{at(21, 20, 0), 21, "B"},  // ровно начало ночной
		{at(21, 23, 59), 21, "B"},
		{at(22, 0, 30), 21, "B"},  // после полуночи — всё ещё ночь 21-го
		{at(22, 7, 59), 21, "B"},  // последняя минута ночной
		{at(22, 8, 0), 22, "A"},   // новая дневная
	}
	for _, tt := range tests {
		s := ShiftOf(tt.t)
		if s.Date.Day() != tt.wantDay || s.Name != tt.wantName {
			t.Errorf("%s: получили %s %s, ожидалось %d %s",
				tt.t.Format("02.01 15:04"), s.Date.Format("02.01"), s.Name, tt.wantDay, tt.wantName)
		}
	}
}

func TestShiftBounds(t *testing.T) {
	night := ShiftOf(at(22, 3, 15))
	if !night.Start().Equal(at(21, 20, 0)) || !night.End().Equal(at(22, 8, 0)) {
		t.Errorf("ночная смена: %v — %v", night.Start(), night.End())
	}
	day := ShiftOf(at(21, 10, 0))
	if !day.Start().Equal(at(21, 8, 0)) || !day.End().Equal(at(21, 20, 0)) {
		t.Errorf("дневная смена: %v — %v", day.Start(), day.End())
	}
}

func TestPath(t *testing.T) {
	s := ShiftOf(at(22, 3, 15))
	got := Path(`D:\plc-data`, "Пресс: 315F", s, at(22, 3, 15), 2)
	want := filepath.Join(`D:\plc-data`, "Пресс_ 315F", "2026", "10", "21", "B_20261022-031500_g2.pla")
	if got != want {
		t.Errorf("путь\n получили %s\n ожидался %s", got, want)
	}

	// Порядок: перезапуск ночью должен сортироваться ПОСЛЕ начала смены
	early := filepath.Base(Path("x", "p", s, at(21, 20, 0), 1))
	late := filepath.Base(Path("x", "p", s, at(22, 3, 15), 2))
	if strings.Compare(early, late) >= 0 {
		t.Errorf("неверный порядок: %s должен быть раньше %s", early, late)
	}
}
