package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestTGNotifyState(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, ok, _ := st.TGNotifyState(ctx); ok {
		t.Error("закладки ещё нет")
	}
	st.SaveTGNotifyState(ctx, 42, []int64{7, 9})
	last, active, ok, err := st.TGNotifyState(ctx)
	if err != nil || !ok || last != 42 || fmt.Sprint(active) != "[7 9]" {
		t.Errorf("закладка: %d %v %v %v", last, active, ok, err)
	}
	st.SaveTGNotifyState(ctx, 43, nil)
	if last, active, ok, _ = st.TGNotifyState(ctx); !ok || last != 43 || len(active) != 0 {
		t.Errorf("без активных: %d %v", last, active)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM changelog`).Scan(&n)
	if n != 0 {
		t.Errorf("закладка попала в журнал изменений: %d", n)
	}
}
