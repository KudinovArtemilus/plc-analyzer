package tgnotify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func setup(t *testing.T) (*store.Store, context.Context, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p1, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	p2, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.2", Slot: 1})
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, Enabled: true, Notify: true, Label: "Шлифовка"})
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p2, Enabled: true, Notify: false}) // показывать, но не уведомлять
	return st, ctx, p1, p2
}

var t0 = time.Date(2026, 11, 18, 9, 58, 0, 0, time.Local)

func edge(sig, msg string, on bool, at time.Time) store.EventEdge {
	return store.EventEdge{Signal: sig, Kind: store.KindAlarm, Message: msg, On: on, At: at}
}

func texts(n []Note) string {
	var s []string
	for _, x := range n {
		s = append(s, fmt.Sprintf("[%v] %s", x.Silent, x.Text))
	}
	return strings.Join(s, "\n---\n")
}

func TestNotes(t *testing.T) {
	st, ctx, p1, p2 := setup(t)
	// Авария до запуска — не присылается
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Старая", "Старая авария", true, t0.Add(-time.Hour))})
	w := New(st)
	if n, _ := w.Notes(ctx, t0); len(n) != 0 {
		t.Fatalf("первый вызов: %s", texts(n))
	}

	// Пришла авария; у «Пресса» тоже, но он без «уведомлять»; предупреждение — не авария
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Дверь", "Дверь открыта", true, t0),
		{Signal: "Смазка", Kind: store.KindWarning, Message: "Мало смазки", On: true, At: t0}})
	st.SaveEdges(ctx, p2, []store.EventEdge{edge("Х", "Авария пресса", true, t0)})
	n, err := w.Notes(ctx, t0.Add(time.Second))
	if err != nil || texts(n) != "[false] 🔴 Шлифовка — Дверь открыта" {
		t.Fatalf("пришла: %v\n%s", err, texts(n))
	}

	// Через 20 с — 12 аварий разом: одно сообщение, сразу (второе по станку за минуту)
	var lav []store.EventEdge
	for i := 1; i <= 12; i++ {
		lav = append(lav, edge(fmt.Sprintf("A%d", i), fmt.Sprintf("Авария %d", i), true, t0.Add(20*time.Second)))
	}
	st.SaveEdges(ctx, p1, lav)
	n, _ = w.Notes(ctx, t0.Add(21*time.Second))
	got := texts(n)
	if len(n) != 1 || !strings.HasPrefix(got, "[false] 🔴 Шлифовка — Авария 1\n🔴 Шлифовка — Авария 2\n") || !strings.HasSuffix(got, "🔴 Шлифовка — Авария 10\n… и ещё 2") {
		t.Errorf("12 разом:\n%s", got)
	}
	// Дверь закрыли через 25 с — «ушла» приходит сразу, без звука (третье за минуту — ещё можно)
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Дверь", "Дверь открыта", false, t0.Add(25*time.Second))})
	n, _ = w.Notes(ctx, t0.Add(26*time.Second))
	if texts(n) != "[true] ✅ Шлифовка — Дверь открыта: ушла, длилась меньше минуты" {
		t.Errorf("ушла:\n%s", texts(n))
	}
	// Четвёртое за минуту — лавина: копится до конца окна (минута от первого сообщения)
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("A1", "Авария 1", false, t0.Add(30*time.Second))})
	if n, _ := w.Notes(ctx, t0.Add(31*time.Second)); len(n) != 0 {
		t.Errorf("четвёртое за минуту ушло сразу: %s", texts(n))
	}
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("A2", "Авария 2", false, t0.Add(40*time.Second))})
	n, _ = w.Notes(ctx, t0.Add(62*time.Second))
	if texts(n) != "[true] ✅ Шлифовка — Авария 1: ушла, длилась меньше минуты\n✅ Шлифовка — Авария 2: ушла, длилась меньше минуты" {
		t.Errorf("после окна:\n%s", texts(n))
	}
}

// Зашунтированная авария молчит; снятая галочка — тоже
func TestNotesShelvedAndOff(t *testing.T) {
	st, ctx, p1, _ := setup(t)
	w := New(st)
	w.Notes(ctx, t0)
	st.AddShelf(ctx, store.Shelf{PLCID: p1, Signal: "Дверь", Reason: "ремонт", Who: "тест", From: t0.Add(-time.Hour), Until: t0.Add(time.Hour)})
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Дверь", "Дверь открыта", true, t0)})
	if n, _ := w.Notes(ctx, t0.Add(time.Second)); len(n) != 0 {
		t.Errorf("зашунтированная: %s", texts(n))
	}
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, Enabled: true, Notify: false})
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Масло", "Перегрев", true, t0.Add(2*time.Second))})
	if n, _ := w.Notes(ctx, t0.Add(3*time.Second)); len(n) != 0 {
		t.Errorf("без «уведомлять»: %s", texts(n))
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "меньше минуты", 12 * time.Minute: "12 мин", 2 * time.Hour: "2 ч", 125 * time.Minute: "2 ч 5 мин",
	} {
		if got := Duration(d); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
}

// Авария по коду: подпись кода из справочника сигнала; кода нет в справочнике — как в журнале
func TestCodeText(t *testing.T) {
	st, ctx, p1, _ := setup(t)
	d, _ := st.AddDict(ctx, "тест", "Ошибки привода")
	st.SetDictItem(ctx, "тест", d, store.DictItem{Value: 7501, Text: "Перегрев двигателя"})
	id, err := st.AddSignal(ctx, "тест", store.SignalRow{PLCID: p1, Name: "Привод", Addr: "DB1.DBW0", Type: "INT",
		PeriodMs: 100, Scale: 1, Enabled: true, Kind: store.KindAlarm, Message: "Ошибка привода"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := st.Signal(ctx, id)
	r.DictID = d
	if err := st.UpdateSignal(ctx, "тест", r); err != nil {
		t.Fatal(err)
	}

	w := New(st)
	w.Notes(ctx, t0)
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Привод", "Ошибка привода: код 7501", true, t0)})
	n, _ := w.Notes(ctx, t0.Add(time.Second))
	if want := "[false] 🔴 Шлифовка — Ошибка привода: Перегрев двигателя (код 7501)"; texts(n) != want {
		t.Errorf("код из справочника:\n%s\nждали:\n%s", texts(n), want)
	}
	// Код сменился на 42, которого нет в справочнике: старая ушла, новая — как в журнале
	st.SaveEdges(ctx, p1, []store.EventEdge{
		edge("Привод", "Ошибка привода: код 7501", false, t0.Add(5*time.Second)),
		edge("Привод", "Ошибка привода: код 42", true, t0.Add(5*time.Second))})
	n, _ = w.Notes(ctx, t0.Add(6*time.Second))
	want := "[false] 🔴 Шлифовка — Ошибка привода: код 42\n✅ Шлифовка — Ошибка привода: Перегрев двигателя (код 7501): ушла, длилась меньше минуты"
	if texts(n) != want {
		t.Errorf("смена кода:\n%s\nждали:\n%s", texts(n), want)
	}
}
