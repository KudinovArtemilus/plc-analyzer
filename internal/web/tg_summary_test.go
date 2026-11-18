package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgbot"
)

// Сейчас 10:00, смена А с 08:00 (120 мин). Запись станка: 08:00–09:00 режим 1 (Автомат),
// 09:00–09:30 режим 2 (Ручной), дальше записи нет (30 мин — «нет связи»).
// Авария 08:30–08:45 (15 мин) и ещё одна активная с 09:58.
func tgSummaryServer(t *testing.T) (*Server, *store.Store, context.Context, time.Time, int64, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	area, _ := st.AddArea(ctx, "тест", "Мехобработка")
	p1, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.10.20", Slot: 2, AreaID: area})
	p2, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.10.21", Slot: 1})

	dict, _ := st.AddDict(ctx, "тест", "Шаги")
	st.SetDictItem(ctx, "тест", dict, store.DictItem{Value: 12, Text: "Зажим закрыт"})
	ids := map[string]int64{}
	for _, r := range []store.SignalRow{
		{Name: "Режим", Addr: "DB103.DBW0", Type: "INT"},
		{Name: "Шаг", Addr: "DB103.DBW2", Type: "INT", DictID: dict},
		{Name: "Давление", Addr: "DB103.DBD4", Type: "REAL"},
		{Name: "Температура", Addr: "DB103.DBD8", Type: "REAL"},
	} {
		r.PLCID, r.PeriodMs, r.Scale, r.Enabled, r.Kind = p1, 1000, 1, true, store.KindTrend
		id, _ := st.AddSignal(ctx, "тест", r)
		r.ID = id
		st.UpdateSignal(ctx, "тест", r)
		ids[r.Name] = id
	}
	st.SavePassport(ctx, "тест", store.Passport{PLCID: p1, Step: "Шаг", Mode: "Режим", ModeVal: 1})
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, Enabled: true, Label: "Шлифовка", ModeSignalID: ids["Режим"],
		Show: store.TGShow{State: true, Step: true, Bar: true, Signals: true, Alarms: true}, AlarmState: true, OfflineState: true})
	a, _ := st.AddTGState(ctx, "тест", store.TGState{PLCID: p1, Name: "Автомат", Color: "blue", Pct: true})
	m, _ := st.AddTGState(ctx, "тест", store.TGState{PLCID: p1, Name: "Ручной", Color: "steel", Pct: true})
	st.SetTGStateCode(ctx, "тест", p1, a, 1, true)
	st.SetTGStateCode(ctx, "тест", p1, m, 2, true)
	st.AddTGSignal(ctx, "тест", p1, ids["Давление"])
	st.UpdateTGSignal(ctx, "тест", store.TGSignal{PLCID: p1, SignalID: ids["Давление"], How: "minmax", Label: "Давление, бар"})
	st.AddTGSignal(ctx, "тест", p1, ids["Шаг"])
	st.UpdateTGSignal(ctx, "тест", store.TGSignal{PLCID: p1, SignalID: ids["Шаг"], How: "dict", Label: "Шаг"})
	// «Пресс» с подписью, но выключен — его не должно быть в сводке
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p2, Enabled: false, Label: "Пресс М10"})

	day := time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	now := at(10, 0)

	// Файл смены: точка в секунду, 08:00–09:30
	root := t.TempDir()
	sh := archive.ShiftOf(now)
	path := archive.Path(root, "SGGT", sh, at(8, 0), 1)
	os.MkdirAll(filepath.Dir(path), 0o755)
	sig := []archive.SignalInfo{{Name: "Режим", Addr: "DB103.DBW0", Type: "INT", Scale: 1},
		{Name: "Давление", Addr: "DB103.DBD4", Type: "REAL", Scale: 1}}
	w, err := archive.Create(path, archive.Header{PLC: "SGGT", Shift: "A", ShiftDate: "2026-11-17",
		Groups: []archive.GroupInfo{{PeriodMs: 1000, Signals: sig}}})
	if err != nil {
		t.Fatal(err)
	}
	c := archive.Chunk{Values: make([][]float64, 2)}
	for tm := at(8, 0); !tm.After(at(9, 30)); tm = tm.Add(time.Second) {
		mode := 1.0
		if !tm.Before(at(9, 0)) {
			mode = 2
		}
		c.Times = append(c.Times, tm.UnixMilli())
		c.Values[0] = append(c.Values[0], mode)
		c.Values[1] = append(c.Values[1], 180)
	}
	w.WriteChunk(c)
	w.Close()

	st.SaveEdges(ctx, p1, []store.EventEdge{
		{Signal: "Перегрев", Kind: store.KindAlarm, Message: "Перегрев масла", On: true, At: at(8, 30)},
		{Signal: "Перегрев", Kind: store.KindAlarm, Message: "Перегрев масла", On: false, At: at(8, 45)},
		{Signal: "Дверь", Kind: store.KindAlarm, Message: "Дверь открыта", On: true, At: at(9, 58)},
		{Signal: "Смазка", Kind: store.KindWarning, Message: "Мало смазки", On: true, At: at(9, 50)},
	})

	// Буфер Live: последние секунды — режим 2, шаг 12, давление 170…190, сейчас 182.444
	ring := buffer.New(100)
	for k, p := range []float64{170, 190, 182.444} {
		ring.Add(buffer.Point{T: now.Add(time.Duration(k-3) * time.Second).UnixMilli(), V: []float64{2, 12, p, 55}})
	}
	pl, _ := st.PLC(ctx, p1)
	s := &Server{Store: st, PLC: pl, DataRoot: root,
		Units: func(id int64) (plc.Status, archive.Status, bool) {
			return plc.Status{Connected: id == p1}, archive.Status{}, true
		}}
	s.SetConfig(p1, 1, []Group{{Period: time.Second, Names: []string{"Режим", "Шаг", "Давление", "Температура"}, Ring: ring}})
	return s, st, ctx, now, p1, root
}

func TestTGSummary(t *testing.T) {
	s, st, ctx, now, p1, _ := tgSummaryServer(t)
	sum, err := s.tgSummary(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Areas) != 1 || sum.Areas[0].Name != "Мехобработка" || len(sum.Areas[0].Machines) != 1 {
		t.Fatalf("участки: %+v", sum.Areas)
	}
	m := sum.Areas[0].Machines[0]
	if m.Name != "Шлифовка" || !m.Online || m.State == nil || m.State.Name != "Авария" || m.Step != "12 · Зажим закрыт" {
		t.Errorf("карточка: %+v state=%+v", m, m.State)
	}
	if len(m.Alarms) != 1 || m.Alarms[0].Text != "Дверь открыта" || m.Alarms[0].Since != "09:58" {
		t.Errorf("аварии (предупреждение не считается): %+v", m.Alarms)
	}
	// Смена 120 мин: Автомат 60−15 = 45, Ручной 30, Авария 15, нет записи 30
	want := "[{Автомат blue 37.5 45} {Ручной steel 25 30} {Авария red 12.5 15} {Нет связи gray 25 30}]"
	if m.Shift == nil || m.Shift.Name != "А" || m.Shift.From != "08:00" || fmtShares(m.Shift.Shares) != want {
		t.Errorf("смена: %+v\nждали %s", m.Shift, want)
	}
	if len(m.Signals) != 2 || *m.Signals[0].Value != 182.44 || *m.Signals[0].Min != 170 || *m.Signals[0].Max != 190 ||
		m.Signals[1].Text != "12 · Зажим закрыт" || m.Signals[1].Value != nil {
		t.Errorf("сигналы: %s", mustJSON(m.Signals))
	}

	// Аварию зашунтировали — станок уже не «в аварии», а в своём режиме
	st.AddShelf(ctx, store.Shelf{PLCID: p1, Signal: "Дверь", Reason: "ремонт", Who: "тест",
		From: now.Add(-time.Minute), Until: now.Add(time.Hour)})
	sum, _ = s.tgSummary(ctx, now)
	if m := sum.Areas[0].Machines[0]; m.State.Name != "Ручной" || len(m.Alarms) != 0 {
		t.Errorf("после шунта: %+v %+v", m.State, m.Alarms)
	}
	// Связи нет — «Нет связи», даже если в буфере есть значения
	s.Units = func(int64) (plc.Status, archive.Status, bool) { return plc.Status{}, archive.Status{}, true }
	sum, _ = s.tgSummary(ctx, now)
	if m := sum.Areas[0].Machines[0]; m.Online || m.State.Name != "Нет связи" {
		t.Errorf("без связи: %+v", m)
	}
	// Значения в буфере старые (минуту назад) — «сейчас» их нет: на телефоне «—», а не старое число
	sum, _ = s.tgSummary(ctx, now.Add(time.Minute))
	if m := sum.Areas[0].Machines[0]; m.Step != "" || m.Signals[0].Value != nil || m.Signals[0].Label != "Давление, бар" {
		t.Errorf("старые значения: %s", mustJSON(m))
	}
}

// Что не отмечено — того нет; и никаких IP, адресов DB и путей к файлам
func TestTGSummaryNothingExtra(t *testing.T) {
	s, st, ctx, now, p1, root := tgSummaryServer(t)
	sum, _ := s.tgSummary(ctx, now)
	b := mustJSON(sum)
	for _, bad := range []string{"192.168", "DB103", "DBW", "DBD", ".pla", root, filepath.ToSlash(root),
		"Пресс", "Температура", "Мало смазки", "Перегрев", `"Signal"`, "ip", "addr", "path"} {
		if strings.Contains(b, bad) {
			t.Errorf("в сводке есть «%s»:\n%s", bad, b)
		}
	}
	// Сняли галочки карточки — полей нет совсем
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, Enabled: true})
	sum, _ = s.tgSummary(ctx, now)
	if b := mustJSON(sum.Areas[0].Machines[0]); b != `{"name":"SGGT","online":true}` {
		t.Errorf("без галочек: %s", b)
	}
	// Выключили станок — сводка пустая
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, Enabled: false})
	sum, _ = s.tgSummary(ctx, now)
	if len(sum.Areas) != 0 {
		t.Errorf("выключенный станок в сводке: %s", mustJSON(sum))
	}
}

// Страница: блок «Что уйдёт наружу» и summary.json — только на этом ПК
func TestTGPreviewPage(t *testing.T) {
	s, _, _, _, _, _ := tgSummaryServer(t)
	h := s.Handler()
	b := tgDo(h, "GET", "/telegram", nil, false).Body.String()
	if !strings.Contains(b, "Что уйдёт наружу") || !strings.Contains(b, "&#34;name&#34;: &#34;Шлифовка&#34;") ||
		!strings.Contains(b, " байт") {
		t.Errorf("нет предпросмотра:\n%s", b)
	}
	w := tgDo(h, "GET", "/telegram/summary.json", nil, false)
	var sum summary.Summary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || sum.Period != 60 {
		t.Errorf("summary.json: %v %s", err, w.Body.String())
	}
	if w := tgDo(h, "GET", "/telegram/summary.json", nil, true); w.Code != 403 {
		t.Errorf("summary.json с чужого ПК: %d", w.Code)
	}
}

func fmtShares(sh []summary.Share) string {
	var parts []string
	for _, x := range sh {
		parts = append(parts, "{"+x.Name+" "+x.Color+" "+trimFloat(x.Pct)+" "+itoa(int64(x.Min))+"}")
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func trimFloat(f float64) string { b, _ := json.Marshal(f); return string(b) }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// Блок «Бот» на странице: не настроен — подсказка с командой; настроен — имя, получатели, ошибка
func TestTGBotBlock(t *testing.T) {
	s, _, _, _, _, _ := tgSummaryServer(t)
	s.TGBot = func() tgbot.Status { return tgbot.Status{} }
	b := tgDo(s.Handler(), "GET", "/telegram", nil, false).Body.String()
	if !strings.Contains(b, "Бот не настроен") || !strings.Contains(b, "plc-analyzer.exe telegram") {
		t.Errorf("не настроен:\n%s", b)
	}
	at := time.Date(2026, 11, 17, 10, 0, 5, 0, time.Local)
	s.TGBot = func() tgbot.Status {
		return tgbot.Status{Configured: true, Bot: "@Status_PMK_bot", Users: 2, Pushed: at, Bytes: 1234,
			Err: "«Табло» для 222: Bad Request: chat not found", ErrAt: at}
	}
	b = tgDo(s.Handler(), "GET", "/telegram", nil, false).Body.String()
	for _, want := range []string{"@Status_PMK_bot", "получателей: 2", "обновлено 10:00:05", "1234 символов", "chat not found"} {
		if !strings.Contains(b, want) {
			t.Errorf("нет «%s»", want)
		}
	}
}
