package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	st.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("версия базы %d, ожидалась %d", v, len(migrations))
	}

	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	_, err = st.AddSignal(ctx, "тест", SignalRow{
		PLCID: plcID, Name: "AL_1", Addr: "DB100.DBX0.0", Type: "BOOL", PeriodMs: 100,
		Scale: 1, Enabled: true, Kind: KindAlarm, Message: "Перегрев масла",
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Повторное открытие: миграции не выполняются второй раз, данные на месте
	st, err = Open(path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer st.Close()

	sigs, _ := st.Signals(ctx, plcID)
	if len(sigs) != 1 || sigs[0].Kind != KindAlarm || sigs[0].Message != "Перегрев масла" {
		t.Fatalf("тип и сообщение не сохранились: %+v", sigs)
	}
}
