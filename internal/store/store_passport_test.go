package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPassport(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p1, _ := st.AddPLC(ctx, "тест", PLC{Name: "p1", IP: "192.168.0.1", Slot: 2})
	p2, _ := st.AddPLC(ctx, "тест", PLC{Name: "p2", IP: "192.168.0.2", Slot: 2})

	// Пока не настроен — ErrNotFound, это не сбой
	if _, err := st.Passport(ctx, p1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("не настроен: %v", err)
	}
	if err := st.SavePassport(ctx, "тест", Passport{PLCID: p1, Step: " "}); !errors.Is(err, ErrPassport) {
		t.Fatalf("без сигнала шага: %v", err)
	}

	before := time.Now().Add(-time.Second)
	want := Passport{PLCID: p1, Step: "Шаги", Mode: "Режим", ModeVal: 1, Home: 0}
	if err := st.SavePassport(ctx, "тест", want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Passport(ctx, p1)
	if err != nil || got.Step != "Шаги" || got.Mode != "Режим" || got.ModeVal != 1 || got.Home != 0 ||
		got.Saved.Before(before) {
		t.Fatalf("прочитали %+v, %v", got, err)
	}

	// Повторное сохранение заменяет строку, а не добавляет вторую
	want.Home, want.Mode = 21, ""
	if err := st.SavePassport(ctx, "тест", want); err != nil {
		t.Fatal(err)
	}
	st.SavePassport(ctx, "тест", Passport{PLCID: p2, Step: "Step", Home: 3})
	all, err := st.Passports(ctx)
	if err != nil || len(all) != 2 || all[0].Home != 21 || all[0].Mode != "" || all[1].PLCID != p2 {
		t.Fatalf("все настройки: %+v, %v", all, err)
	}

	// Удалили ПЛК — его настройка ушла вместе с ним
	if _, err := st.DeletePLC(ctx, "тест", PLC{ID: p2}); err != nil {
		t.Fatal(err)
	}
	if all, _ := st.Passports(ctx); len(all) != 1 {
		t.Errorf("после удаления ПЛК осталось %d настроек", len(all))
	}
}
