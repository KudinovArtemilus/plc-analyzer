package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAddSignals(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	changes := 0
	st.OnChange = func() { changes++ }
	before, _ := st.ChangeCount(ctx)

	rows := []SignalRow{
		{PLCID: plcID, Name: "AL_1", Addr: "DB100.DBX0.0", Type: "BOOL", PeriodMs: 100, Scale: 1, Enabled: true, Kind: KindAlarm, Message: "Дверь"},
		{PLCID: plcID, Name: "P", Addr: "DB1.DBD0", Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true}, // Kind пустой → тренд
	}
	if err := st.AddSignals(ctx, "тест", rows); err != nil {
		t.Fatal(err)
	}
	sigs, _ := st.Signals(ctx, plcID)
	if len(sigs) != 2 || sigs[1].Kind != KindTrend {
		t.Fatalf("после добавления: %+v", sigs)
	}
	// Опрос перестраивается один раз, в журнале изменений — одна запись на всю пачку
	after, _ := st.ChangeCount(ctx)
	if changes != 1 || after-before != 1 {
		t.Fatalf("OnChange %d раз, записей в журнале %d; ожидалось 1 и 1", changes, after-before)
	}

	// Повтор имени в пачке — не добавляется НИЧЕГО (одна транзакция)
	bad := []SignalRow{
		{PLCID: plcID, Name: "X", Addr: "DB1.DBD4", Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true},
		{PLCID: plcID, Name: "P", Addr: "DB1.DBD8", Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true},
	}
	if err := st.AddSignals(ctx, "тест", bad); err == nil {
		t.Fatal("повтор имени должен дать ошибку")
	}
	if sigs, _ = st.Signals(ctx, plcID); len(sigs) != 2 {
		t.Fatalf("после ошибки сигналов %d, ожидалось 2: транзакция должна откатиться", len(sigs))
	}
}
