package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSignalHidden(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	id, _ := st.AddSignal(ctx, "тест", SignalRow{PLCID: plcID, Name: "P", Addr: "DB1.DBD0",
		Type: "REAL", PeriodMs: 100, Scale: 1, Enabled: true})

	// Новый сигнал виден на графиках
	sig, _ := st.Signal(ctx, id)
	if sig.Hidden {
		t.Fatal("новый сигнал не должен быть скрыт")
	}

	sig.Hidden = true
	if err := st.SetSignalHidden(ctx, "тест", sig); err != nil {
		t.Fatal(err)
	}
	sig, _ = st.Signal(ctx, id)
	if !sig.Hidden || !sig.Enabled {
		t.Fatalf("после скрытия: скрыт %v, включён %v; ожидалось скрыт и ВКЛЮЧЁН (опрос идёт)", sig.Hidden, sig.Enabled)
	}

	// Правка сигнала (имя, адрес…) не сбрасывает «скрыт»: UpdateSignal его не трогает
	sig.Unit = "бар"
	sig.Hidden = false // даже если форма прислала false
	if err := st.UpdateSignal(ctx, "тест", sig); err != nil {
		t.Fatal(err)
	}
	if sig, _ = st.Signal(ctx, id); !sig.Hidden || sig.Unit != "бар" {
		t.Fatalf("после правки: %+v", sig)
	}
}
