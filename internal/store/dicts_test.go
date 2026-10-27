package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDicts(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Справочник: пустое имя и повтор имени — нельзя
	if _, err := st.AddDict(ctx, "тест", "  "); !errors.Is(err, ErrDictName) {
		t.Fatalf("пустое имя: %v", err)
	}
	id, err := st.AddDict(ctx, "тест", "Шаги")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDict(ctx, "тест", "Шаги"); !errors.Is(err, ErrDictName) {
		t.Fatalf("повтор имени: %v", err)
	}

	// Строки: добавить, заменить текст (UPSERT), пустой текст — нельзя
	st.SetDictItem(ctx, "тест", id, DictItem{Value: 12, Text: "Зажим закрыт"})
	st.SetDictItem(ctx, "тест", id, DictItem{Value: 3, Text: "Исходное"})
	st.SetDictItem(ctx, "тест", id, DictItem{Value: 12, Text: "  Зажим закрыт, ждём стол "})
	if err := st.SetDictItem(ctx, "тест", id, DictItem{Value: 5, Text: " "}); err == nil {
		t.Fatal("пустой текст должен быть отклонён")
	}
	items, _ := st.DictItems(ctx, id)
	if len(items) != 2 || items[0].Value != 3 || items[1].Text != "Зажим закрыт, ждём стол" {
		t.Fatalf("строки (по возрастанию, текст обрезан): %+v", items)
	}

	// Назначить справочник сигналу
	plcID, _ := st.AddPLC(ctx, "тест", PLC{Name: "p", IP: "192.168.0.1", Slot: 2})
	sid, _ := st.AddSignal(ctx, "тест", SignalRow{PLCID: plcID, Name: "Step", Addr: "DB1.DBW0",
		Type: "INT", PeriodMs: 100, Scale: 1, Enabled: true})
	sig, _ := st.Signal(ctx, sid)
	if sig.DictID != 0 {
		t.Fatalf("новый сигнал без справочника: DictID %d", sig.DictID)
	}
	sig.DictID = id
	if err := st.UpdateSignal(ctx, "тест", sig); err != nil {
		t.Fatal(err)
	}
	if sig, _ = st.Signal(ctx, sid); sig.DictID != id {
		t.Fatalf("справочник не назначен: %d", sig.DictID)
	}
	list, _ := st.Dicts(ctx)
	if len(list) != 1 || list[0].Items != 2 || list[0].Used != 1 {
		t.Fatalf("список справочников: %+v", list)
	}

	// Тексты разом — для опроса и графиков
	texts, _ := st.DictTexts(ctx, []int64{id, 0, id})
	if texts[id][12] != "Зажим закрыт, ждём стол" || len(texts) != 1 {
		t.Fatalf("тексты: %+v", texts)
	}

	// Строку удалить
	if err := st.DeleteDictItem(ctx, "тест", id, 3); err != nil {
		t.Fatal(err)
	}
	// Справочник удалить: строки уходят вместе с ним, у сигнала ссылка обнуляется, сам сигнал остаётся
	if err := st.DeleteDict(ctx, "тест", list[0]); err != nil {
		t.Fatal(err)
	}
	if sig, err = st.Signal(ctx, sid); err != nil || sig.DictID != 0 {
		t.Fatalf("после удаления справочника: %+v, %v", sig, err)
	}
	if items, _ = st.DictItems(ctx, id); len(items) != 0 {
		t.Fatalf("строки удалённого справочника остались: %+v", items)
	}
}
