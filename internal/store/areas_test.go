package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAreas(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Создать: по порядку; пустое, слишком длинное и повтор — нельзя
	mech, err := st.AddArea(ctx, "тест", "  Мехобработка ")
	if err != nil {
		t.Fatal(err)
	}
	press, _ := st.AddArea(ctx, "тест", "Прессовый")
	if _, err := st.AddArea(ctx, "тест", " "); !errors.Is(err, ErrAreaName) {
		t.Errorf("пустое название: %v", err)
	}
	if _, err := st.AddArea(ctx, "тест", strings.Repeat("я", AreaNameMax+1)); !errors.Is(err, ErrAreaName) {
		t.Errorf("длинное название: %v", err)
	}
	if _, err := st.AddArea(ctx, "тест", "Прессовый"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("повтор названия: %v", err)
	}
	names := func() string {
		list, _ := st.Areas(ctx)
		var s []string
		for _, a := range list {
			s = append(s, a.Name)
		}
		return strings.Join(s, ",")
	}
	if names() != "Мехобработка,Прессовый" {
		t.Fatalf("порядок: %s", names())
	}

	// ПЛК: в участке и без; в одном участке — несколько ПЛК
	p1, _ := st.AddPLC(ctx, "тест", PLC{Name: "Станок 1", IP: "192.168.0.1", Slot: 2, AreaID: mech})
	p2, _ := st.AddPLC(ctx, "тест", PLC{Name: "Станок 2", IP: "192.168.0.2", Slot: 2, AreaID: mech})
	p3, _ := st.AddPLC(ctx, "тест", PLC{Name: "Пресс", IP: "192.168.0.3", Slot: 1})
	if p, _ := st.PLC(ctx, p1); p.AreaID != mech || p.Area != "Мехобработка" {
		t.Errorf("ПЛК 1: участок %d %q", p.AreaID, p.Area)
	}
	if p, _ := st.PLC(ctx, p3); p.AreaID != 0 || p.Area != "" {
		t.Errorf("ПЛК без участка: %d %q", p.AreaID, p.Area)
	}
	if a, _ := st.Area(ctx, mech); a.PLCs != 2 {
		t.Errorf("в участке ПЛК: %d", a.PLCs)
	}

	// Перенести ПЛК в другой участок — через UpdatePLC, в журнале — названиями
	old, _ := st.PLC(ctx, p3)
	n := old
	n.AreaID = press
	if err := st.UpdatePLC(ctx, "тест", old, n); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PLC(ctx, p3); p.Area != "Прессовый" {
		t.Errorf("перенос: %q", p.Area)
	}
	var details string
	st.db.QueryRow(`SELECT details FROM changelog ORDER BY id DESC LIMIT 1`).Scan(&details)
	if !strings.Contains(details, "участок: без участка → Прессовый") {
		t.Errorf("журнал: %q", details)
	}
	// Несуществующий участок — база не даёт (внешний ключ)
	bad := n
	bad.AreaID = 999
	if err := st.UpdatePLC(ctx, "тест", n, bad); err == nil {
		t.Error("участок 999 должен быть отклонён")
	}

	// Порядок: «Прессовый» выше; крайний дальше не двигается
	if err := st.MoveArea(ctx, "тест", press, -1); err != nil {
		t.Fatal(err)
	}
	st.MoveArea(ctx, "тест", press, -1)
	if names() != "Прессовый,Мехобработка" {
		t.Errorf("после «выше»: %s", names())
	}

	// Переименовать
	a, _ := st.Area(ctx, mech)
	if err := st.RenameArea(ctx, "тест", a, "Механообработка"); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.PLC(ctx, p2); p.Area != "Механообработка" {
		t.Errorf("после переименования у ПЛК: %q", p.Area)
	}

	// Удалить участок: ПЛК остаются, но «без участка»; опрос это не трогает
	a, _ = st.Area(ctx, mech)
	if err := st.DeleteArea(ctx, "тест", a); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{p1, p2} {
		if p, err := st.PLC(ctx, id); err != nil || p.AreaID != 0 {
			t.Errorf("ПЛК %d после удаления участка: %v %d", id, err, p.AreaID)
		}
	}
	if _, err := st.Area(ctx, mech); !errors.Is(err, ErrNotFound) {
		t.Errorf("удалённый участок: %v", err)
	}
}
