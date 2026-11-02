package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPLCPDU(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Не указан — 240
	id, err := st.AddPLC(ctx, "тест", PLC{Name: "Пресс", IP: "192.168.0.1", Slot: 1})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.PLC(ctx, id)
	if p.PDU != DefaultPDU {
		t.Fatalf("PDU по умолчанию %d", p.PDU)
	}

	// Поменяли на 480 — читается и в PLC, и в PLCs
	n := p
	n.PDU = 480
	if err := st.UpdatePLC(ctx, "тест", p, n); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.PLC(ctx, id); p.PDU != 480 {
		t.Errorf("после правки PDU %d", p.PDU)
	}
	all, _ := st.PLCs(ctx)
	for _, a := range all {
		if a.ID == id && a.PDU != 480 {
			t.Errorf("PLCs: PDU %d", a.PDU)
		}
	}
}
