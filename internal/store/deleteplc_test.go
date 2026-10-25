package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDeletePLC(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p1 := PLC{Name: "С1", IP: "192.168.0.1", Slot: 2}
	p1.ID, _ = st.AddPLC(ctx, "тест", p1)

	// Единственный ПЛК удалить нельзя
	if _, err := st.DeletePLC(ctx, "тест", p1); !errors.Is(err, ErrLastPLC) {
		t.Fatalf("удаление единственного ПЛК: %v, ожидалось ErrLastPLC", err)
	}

	p2 := PLC{Name: "С2", IP: "192.168.0.2", Slot: 2}
	p2.ID, _ = st.AddPLC(ctx, "тест", p2)

	// У С2 есть всё, что ссылается на ПЛК: сигналы, авария, шунт, слепок
	now := time.Now()
	for _, n := range []string{"AL1", "T1"} {
		if _, err := st.AddSignal(ctx, "тест", SignalRow{PLCID: p2.ID, Name: n, Addr: "DB1.DBX0.0",
			Type: "BOOL", PeriodMs: 100, Scale: 1, Enabled: true, Kind: KindAlarm, Message: "м"}); err != nil {
			t.Fatal(err)
		}
	}
	st.SaveEdges(ctx, p2.ID, []EventEdge{{Signal: "AL1", Kind: KindAlarm, Message: "м", On: true, At: now}})
	st.AddShelf(ctx, Shelf{PLCID: p2.ID, Signal: "AL1", Reason: "р", Who: "т", From: now, Until: now.Add(time.Hour)})
	st.AddSnapshot(ctx, "тест", Snapshot{PLCID: p2.ID, Name: "с", File: "snapshots/S_1.pla", Signals: []string{"AL1"}})
	// И у С1 сигнал — его трогать нельзя
	st.AddSignal(ctx, "тест", SignalRow{PLCID: p1.ID, Name: "X", Addr: "DB1.DBW0", Type: "INT",
		PeriodMs: 100, Scale: 1, Enabled: true})

	changes := 0
	st.OnChange = func() { changes++ }

	n, err := st.DeletePLC(ctx, "тест", p2)
	if err != nil || n != 2 {
		t.Fatalf("удаление С2: сигналов %d, ошибка %v; ожидалось 2 и без ошибки", n, err)
	}
	if changes != 1 {
		t.Fatalf("OnChange вызван %d раз — должен 1 раз, чтобы менеджер остановил опрос", changes)
	}
	if _, err := st.PLC(ctx, p2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("С2 должен исчезнуть: %v", err)
	}
	// Ничего от С2 не осталось
	for _, table := range []string{"signals", "events", "shelves", "snapshots"} {
		var left int
		st.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE plc_id = ?`, p2.ID).Scan(&left)
		if left != 0 {
			t.Fatalf("в %s осталось %d строк удалённого ПЛК", table, left)
		}
	}
	// А у С1 всё на месте
	if sigs, _ := st.Signals(ctx, p1.ID); len(sigs) != 1 {
		t.Fatalf("сигналы С1 пострадали: %d", len(sigs))
	}
}
