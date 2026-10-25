package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestShelves(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	changes := 0
	st.OnChange = func() { changes++ }
	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	changes = 0

	t0 := time.Date(2026, 10, 25, 8, 0, 0, 0, time.Local)
	shelf := func(sig string, d time.Duration) Shelf {
		return Shelf{PLCID: plcID, Signal: sig, Reason: "ждём запчасть", Who: "тест", From: t0, Until: t0.Add(d)}
	}

	// Проверки входных данных
	bad := shelf("AL1", time.Hour)
	bad.Reason = ""
	if _, err := st.AddShelf(ctx, bad); err == nil {
		t.Fatal("шунт без причины должен быть отклонён")
	}
	if _, err := st.AddShelf(ctx, shelf("AL1", 8*24*time.Hour)); err == nil {
		t.Fatal("шунт дольше недели должен быть отклонён")
	}

	id1, err := st.AddShelf(ctx, shelf("AL1", time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShelf(ctx, shelf("AL1", 2*time.Hour)); !errors.Is(err, ErrShelved) {
		t.Fatalf("второй шунт на ту же аварию: %v, ожидалось ErrShelved", err)
	}
	if _, err := st.AddShelf(ctx, shelf("AL2", 4*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Через 30 мин действуют оба, раньше истекает AL1 — он первый
	act, _ := st.ActiveShelves(ctx, plcID, t0.Add(30*time.Minute))
	if len(act) != 2 || act[0].Signal != "AL1" || !act[0].ActiveAt(t0.Add(30*time.Minute)) {
		t.Fatalf("активные через 30 мин: %+v", act)
	}

	// Через 2 ч шунт AL1 истёк сам — авария вернулась на панель
	act, _ = st.ActiveShelves(ctx, plcID, t0.Add(2*time.Hour))
	if len(act) != 1 || act[0].Signal != "AL2" {
		t.Fatalf("через 2 ч должен остаться только AL2: %+v", act)
	}
	// Истёкший снять нельзя
	if err := st.RemoveShelf(ctx, "тест", id1, t0.Add(2*time.Hour)); err == nil {
		t.Fatal("снятие истёкшего шунта должно давать ошибку")
	}

	// AL2 сняли вручную раньше срока
	if err := st.RemoveShelf(ctx, "дежурный", act[0].ID, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if act, _ = st.ActiveShelves(ctx, plcID, t0.Add(3*time.Hour)); len(act) != 0 {
		t.Fatalf("после снятия шунтов быть не должно: %+v", act)
	}

	// История: оба шунта, у AL2 — кто и когда снял
	all, _ := st.RecentShelves(ctx, plcID, t0.Add(-time.Hour))
	if len(all) != 2 {
		t.Fatalf("история: %d шунтов, ожидалось 2", len(all))
	}
	for _, sh := range all {
		if sh.Signal == "AL2" && (sh.RemovedBy != "дежурный" || !sh.Removed.Equal(t0.Add(3*time.Hour))) {
			t.Fatalf("AL2: снял %q в %v", sh.RemovedBy, sh.Removed)
		}
	}

	// В журнале изменений: 2 шунта + 1 снятие (+1 добавление ПЛК)
	if n, _ := st.ChangeCount(ctx); n != 4 {
		t.Fatalf("записей в журнале изменений %d, ожидалось 4", n)
	}
	if changes != 0 {
		t.Fatalf("OnChange вызван %d раз — шунт не должен перестраивать опрос", changes)
	}
}
