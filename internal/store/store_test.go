package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	ctx := context.Background()

	// Временная папка: Go сам удалит её после теста
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plcID, err := st.AddPLC(ctx, "тест", PLC{Name: "Пресс", IP: "10.0.0.1", Slot: 2})
	if err != nil {
		t.Fatal(err)
	}

	sig := SignalRow{PLCID: plcID, Name: "pressure", Addr: "DB11.DBW242",
		Type: "INT", PeriodMs: 100, Scale: 1, Enabled: true}
	if _, err := st.AddSignal(ctx, "тест", sig); err != nil {
		t.Fatal(err)
	}

	// Читаем обратно
	sigs, err := st.Signals(ctx, plcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || sigs[0].Addr != "DB11.DBW242" || !sigs[0].Enabled {
		t.Fatalf("прочитали не то: %+v", sigs)
	}

	// Повтор имени на том же ПЛК — ошибка
	if _, err := st.AddSignal(ctx, "тест", sig); err == nil {
		t.Error("ожидалась ошибка на повторное имя")
	}

	// Сигнал для несуществующего ПЛК — ошибка
	bad := sig
	bad.PLCID, bad.Name = 999, "other"
	if _, err := st.AddSignal(ctx, "тест", bad); err == nil {
		t.Error("ожидалась ошибка для несуществующего ПЛК")
	}

	// В журнале только 2 успешных действия: неудачные откатились вместе с журналом
	n, err := st.ChangeCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("записей в журнале %d, ожидалось 2", n)
	}
}
