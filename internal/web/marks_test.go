package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestMarksOfLegacy(t *testing.T) {
	// Старый слепок по аварии: отметок в базе нет, моменты — в комментарии; слепок через полночь
	from := time.Date(2026, 10, 30, 23, 58, 0, 0, time.Local)
	sn := store.Snapshot{Category: "авария", FromMs: from.UnixMilli(),
		Comment: "23:59:30.250  AL1 — Перегрев\n23:59:31.000  AL1 — Перегрев: код 7\n00:00:10.000  AL2 — Давление\nмусор без времени"}
	m := marksOf(sn)
	if len(m) != 2 || m[0].Text != "AL1 — Перегрев" ||
		m[0].Ms != time.Date(2026, 10, 30, 23, 59, 30, 250e6, time.Local).UnixMilli() ||
		m[1].Ms != time.Date(2026, 10, 31, 0, 0, 10, 0, time.Local).UnixMilli() {
		t.Fatalf("отметки из комментария: %+v", m)
	}
	// Ручной слепок без отметок — комментарий не разбираем
	if m := marksOf(store.Snapshot{Category: "наладка", Comment: sn.Comment}); len(m) != 0 {
		t.Errorf("ручной слепок: %+v", m)
	}
}

func TestSnapshotMarksAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plcID, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local).UnixMilli()
	st.AddSnapshot(ctx, "система", store.Snapshot{PLCID: plcID, Name: "Авария: EStop", Category: "авария",
		File: "snapshots/A_1.pla", FromMs: at - 300_000, ToMs: at + 120_000, Signals: []string{"P", "EStop"},
		Marks: []store.Mark{{Ms: at, Text: "EStop — Аварийный останов"}}})
	pl, _ := st.PLC(ctx, plcID)
	h := (&Server{Store: st, PLC: pl}).Handler()

	do := func(method, target, from, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.RemoteAddr = from + ":5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	q := "?plc=" + itoa(plcID) + "&file=" + url.QueryEscape("snapshots/A_1.pla")
	read := func() []store.Mark {
		var m []store.Mark
		json.Unmarshal(do("GET", "/api/snapshots/marks"+q, "10.0.0.99", "").Body.Bytes(), &m)
		return m
	}

	if m := read(); len(m) != 1 || m[0].Ms != at {
		t.Fatalf("отметки: %+v", m)
	}
	// Запись смены — не слепок: пустой список, не ошибка
	if b := do("GET", "/api/snapshots/marks?plc="+itoa(plcID)+"&file=2026/10/31/A_1.pla", "10.0.0.99", "").Body.String(); strings.TrimSpace(b) != "[]" {
		t.Errorf("запись смены: %s", b)
	}

	mark := `{"ms":` + itoa(at-20_000) + `,"text":"давление начало падать"}`
	// Чужой ПК отметить не может
	do("POST", "/api/snapshots/marks"+q, "10.0.0.99", mark)
	if len(read()) != 1 {
		t.Fatal("чужой ПК поставил отметку")
	}
	if w := do("POST", "/api/snapshots/marks"+q, "127.0.0.1", mark); w.Code != 200 {
		t.Fatalf("отметка: код %d, %s", w.Code, w.Body.String())
	}
	if m := read(); len(m) != 2 || m[1].Text != "давление начало падать" {
		t.Fatalf("после отметки: %+v", m)
	}
	// Вне участка слепка — нельзя
	if w := do("POST", "/api/snapshots/marks"+q, "127.0.0.1", `{"ms":`+itoa(at+999_000)+`}`); w.Code != http.StatusBadRequest {
		t.Errorf("вне участка: код %d", w.Code)
	}

	// Список слепков: ссылка открывает участок вокруг первой отметки, курсор A на ней
	b := do("GET", "/ui/snapshots?plc="+itoa(plcID), "10.0.0.99", "").Body.String()
	for _, want := range []string{"ca=" + itoa(at), "from=" + itoa(at-60_000), "to=" + itoa(at+20_000), "⚑ 09:00:00 · ещё 1"} {
		if !strings.Contains(b, want) {
			t.Errorf("список слепков: нет %q", want)
		}
	}
}
