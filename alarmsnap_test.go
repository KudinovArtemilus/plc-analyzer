package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/events"
)

func TestRingChunks(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	ms := func(sec int) int64 { return t0.Add(time.Duration(sec) * time.Second).UnixMilli() }

	// Группа 0: 2 сигнала, точка каждую секунду; группа 1: 1 сигнал, данных в окне нет
	g0 := buffer.New(100)
	for s := 0; s < 20; s++ {
		g0.Add(buffer.Point{T: ms(s), V: []float64{float64(s), float64(-s)}})
	}
	g0.Add(buffer.Point{T: ms(20), V: []float64{1}}) // от старой конфигурации — другой длины
	g1 := buffer.New(10)
	g1.Add(buffer.Point{T: ms(100), V: []float64{7}})

	chunks := ringChunks([]*buffer.Ring{g0, g1}, []int{2, 1}, t0.Add(5*time.Second), t0.Add(9*time.Second))
	if len(chunks) != 1 || chunks[0].Group != 0 {
		t.Fatalf("ожидалась одна порция группы 0, получено %+v", chunks)
	}
	c := chunks[0]
	// Границы окна включительно: 5, 6, 7, 8, 9
	if len(c.Times) != 5 || c.Times[0] != ms(5) || c.Times[4] != ms(9) {
		t.Fatalf("время точек %v", c.Times)
	}
	if c.Values[0][0] != 5 || c.Values[1][4] != -9 {
		t.Fatalf("значения %v — столбцы по сигналам перепутаны", c.Values)
	}

	// Порция должна записываться в .pla и читаться обратно
	path := filepath.Join(t.TempDir(), "a.pla")
	h := archive.Header{PLC: "p", Groups: []archive.GroupInfo{
		{PeriodMs: 1000, Signals: []archive.SignalInfo{{Name: "A"}, {Name: "B"}}},
		{PeriodMs: 1000, Signals: []archive.SignalInfo{{Name: "C"}}},
	}}
	if err := writePla(path, h, chunks); err != nil {
		t.Fatal(err)
	}
	_, back, err := archive.ReadAll(path)
	if err != nil || len(back) != 1 || len(back[0].Times) != 5 {
		t.Fatalf("чтение слепка: %v, порций %d", err, len(back))
	}
}

func TestSnapshotText(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 150e6, time.Local)
	w := &events.Window{Alarms: []events.Edge{
		{Bit: events.Bit{Signal: "AL1", Message: "Перегрев масла"}, At: at},
		{Bit: events.Bit{Signal: "AL2", Message: "Давление"}, At: at.Add(time.Second)},
	}}
	name, comment := snapshotText(w)
	if name != "Авария: Перегрев масла (+1)" {
		t.Fatalf("название %q", name)
	}
	if !strings.Contains(comment, "09:00:00.150  AL1 — Перегрев масла") || !strings.Contains(comment, "AL2") {
		t.Fatalf("комментарий %q", comment)
	}
}
