package archive

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	DayStart   = 8  // 08:00 — начало смены А (дневной)
	NightStart = 20 // 20:00 — начало смены Б (ночной)
)

// Shift — смена: дата её начала и буква
type Shift struct {
	Date time.Time // полночь дня, в который смена началась (местное время)
	Name string    // "A" — дневная, "B" — ночная
}

// ShiftOf — к какой смене относится момент t (по местному времени ПК)
func ShiftOf(t time.Time) Shift {
	t = t.Local()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch h := t.Hour(); {
	case h < DayStart:
		return Shift{Date: day.AddDate(0, 0, -1), Name: "B"} // ночь, начавшаяся вчера
	case h < NightStart:
		return Shift{Date: day, Name: "A"}
	default:
		return Shift{Date: day, Name: "B"}
	}
}

// Start — момент начала смены
func (s Shift) Start() time.Time {
	h := DayStart
	if s.Name == "B" {
		h = NightStart
	}
	return time.Date(s.Date.Year(), s.Date.Month(), s.Date.Day(), h, 0, 0, 0, s.Date.Location())
}

// End — момент окончания смены (он же начало следующей)
func (s Shift) End() time.Time {
	if s.Name == "A" {
		return time.Date(s.Date.Year(), s.Date.Month(), s.Date.Day(), NightStart, 0, 0, 0, s.Date.Location())
	}
	next := s.Date.AddDate(0, 0, 1)
	return time.Date(next.Year(), next.Month(), next.Day(), DayStart, 0, 0, 0, next.Location())
}

// Title — название смены для экрана
func (s Shift) Title() string {
	if s.Name == "A" {
		return "Смена А"
	}
	return "Смена Б"
}

// Path — путь файла-сегмента:
// root\315F\2026\10\21\A_20261021-080000_g1.pla
func Path(root, plcName string, s Shift, started time.Time, gen int) string {
	return filepath.Join(root, SafeName(plcName),
		s.Date.Format("2006"), s.Date.Format("01"), s.Date.Format("02"),
		fmt.Sprintf("%s_%s_g%d.pla", s.Name, started.Local().Format("20060102-150405"), gen))
}

// SafeName заменяет символы, запрещённые Windows в именах папок
func SafeName(name string) string {
	r := strings.NewReplacer(`\`, "_", "/", "_", ":", "_", "*", "_",
		"?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")
	s := strings.TrimSpace(r.Replace(name))
	if s == "" {
		s = "plc"
	}
	return s
}
