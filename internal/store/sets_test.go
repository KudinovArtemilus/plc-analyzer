package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSets(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Два ПЛК: у первого три сигнала, у второго один
	p1, _ := st.AddPLC(ctx, "тест", PLC{Name: "p1", IP: "192.168.0.1", Slot: 2})
	p2, _ := st.AddPLC(ctx, "тест", PLC{Name: "p2", IP: "192.168.0.2", Slot: 2})
	add := func(plc int64, name string) int64 {
		id, err := st.AddSignal(ctx, "тест", SignalRow{PLCID: plc, Name: name, Addr: "DB1.DBW0",
			Type: "INT", PeriodMs: 100, Scale: 1, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	press, temp, step := add(p1, "Pressure"), add(p1, "OilTemp"), add(p1, "Step")
	other := add(p2, "Other")

	// Имя: пустое и повтор у того же ПЛК — нельзя; у другого ПЛК — можно
	if _, err := st.AddSet(ctx, "тест", p1, " "); !errors.Is(err, ErrSetName) {
		t.Fatalf("пустое имя: %v", err)
	}
	hyd, err := st.AddSet(ctx, "тест", p1, "Гидравлика")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSet(ctx, "тест", p1, "Гидравлика"); !errors.Is(err, ErrSetName) {
		t.Fatalf("повтор имени: %v", err)
	}
	if _, err := st.AddSet(ctx, "тест", p2, "Гидравлика"); err != nil {
		t.Fatalf("то же имя у другого ПЛК должно быть можно: %v", err)
	}
	empty, _ := st.AddSet(ctx, "тест", p1, "Аварии")

	// Состав: порядок как на странице «Сигналы» (по номеру), чужой сигнал не попадает
	err = st.SaveSet(ctx, "тест", SignalSet{ID: hyd, PLCID: p1, Name: " Гидростанция ",
		Signals: []int64{temp, press, other}})
	if err != nil {
		t.Fatal(err)
	}
	set, err := st.Set(ctx, hyd)
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != "Гидростанция" || !reflect.DeepEqual(set.Names, []string{"Pressure", "OilTemp"}) {
		t.Fatalf("набор: %+v", set)
	}

	// Список ПЛК: по имени, пустой набор тоже есть
	list, _ := st.Sets(ctx, p1)
	if len(list) != 2 || list[0].Name != "Аварии" || list[0].ID != empty || len(list[0].Names) != 0 ||
		list[1].ID != hyd || len(list[1].Signals) != 2 {
		t.Fatalf("наборы ПЛК: %+v", list)
	}

	// Новый состав заменяет старый
	st.SaveSet(ctx, "тест", SignalSet{ID: hyd, PLCID: p1, Name: "Гидростанция", Signals: []int64{step}})
	if set, _ = st.Set(ctx, hyd); !reflect.DeepEqual(set.Names, []string{"Step"}) {
		t.Fatalf("после замены: %v", set.Names)
	}

	// Переименовать в занятое имя — ошибка, состав не меняется (транзакция откатилась)
	err = st.SaveSet(ctx, "тест", SignalSet{ID: hyd, PLCID: p1, Name: "Аварии", Signals: []int64{press}})
	if !errors.Is(err, ErrSetName) {
		t.Fatalf("занятое имя: %v", err)
	}
	if set, _ = st.Set(ctx, hyd); set.Name != "Гидростанция" || !reflect.DeepEqual(set.Names, []string{"Step"}) {
		t.Fatalf("после неудачного сохранения набор изменился: %+v", set)
	}

	// Удалили сигнал — из набора исчез, набор остался
	if err := st.DeleteSignal(ctx, "тест", SignalRow{ID: step, Name: "Step"}); err != nil {
		t.Fatal(err)
	}
	if set, err = st.Set(ctx, hyd); err != nil || len(set.Names) != 0 {
		t.Fatalf("после удаления сигнала: %+v, %v", set, err)
	}

	// Удалить набор
	if err := st.DeleteSet(ctx, "тест", set); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Set(ctx, hyd); !errors.Is(err, ErrNotFound) {
		t.Fatalf("удалённый набор: %v", err)
	}

	// Удалили ПЛК — его наборы ушли вместе с ним
	if _, err := st.DeletePLC(ctx, "тест", PLC{ID: p2, Name: "p2"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Sets(ctx, p2); len(list) != 0 {
		t.Fatalf("наборы удалённого ПЛК: %+v", list)
	}
}
