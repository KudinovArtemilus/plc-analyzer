package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// tgServer — сервер с настоящей базой: участок «Мехобработка» со станком, второй ПЛК без участка.
// У станка: режим (INT со справочником 1 Автомат, 2 Ручной, 3 Наладка), бит «Авто», давление (REAL).
func tgServer(t *testing.T) (*store.Store, http.Handler, int64, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "tg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	area, _ := st.AddArea(ctx, "тест", "Мехобработка")
	p1, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Станок", IP: "192.168.0.1", Slot: 2, AreaID: area})
	st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.2", Slot: 1})

	dict, _ := st.AddDict(ctx, "тест", "Режимы")
	for v, text := range map[int64]string{1: "Автомат", 2: "Ручной", 3: "Наладка"} {
		st.SetDictItem(ctx, "тест", dict, store.DictItem{Value: v, Text: text})
	}
	sig := map[string]int64{}
	for _, r := range []store.SignalRow{
		{Name: "Режим", Addr: "DB1.DBW0", Type: "INT", DictID: dict},
		{Name: "Авто", Addr: "DB1.DBX2.0", Type: "BOOL"},
		{Name: "Давление", Addr: "DB1.DBD4", Type: "REAL"},
	} {
		r.PLCID, r.PeriodMs, r.Scale, r.Enabled, r.Kind = p1, 100, 1, true, store.KindTrend
		id, err := st.AddSignal(ctx, "тест", r)
		if err != nil {
			t.Fatal(err)
		}
		r.ID = id
		st.UpdateSignal(ctx, "тест", r) // справочник ставится правкой сигнала
		sig[r.Name] = id
	}
	st.SavePassport(ctx, "тест", store.Passport{PLCID: p1, Step: "Давление", Mode: "Режим", ModeVal: 1})
	pl, _ := st.PLC(ctx, p1)
	return st, (&Server{Store: st, PLC: pl}).Handler(), p1, sig
}

// tgDo — запрос от этого ПК (или с чужого, если remote)
func tgDo(h http.Handler, method, target string, form url.Values, remote bool) *httptest.ResponseRecorder {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.RemoteAddr = "127.0.0.1:50000"
	if remote {
		r.RemoteAddr = "192.168.5.5:50000"
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Чужой ПК страницу не видит и ничего не меняет
func TestTGPageOnlyHere(t *testing.T) {
	_, h, p1, _ := tgServer(t)
	for _, target := range []string{"/telegram", "/telegram/" + itoa(p1)} {
		if w := tgDo(h, "GET", target, nil, true); w.Code != http.StatusForbidden {
			t.Errorf("GET %s с чужого ПК: %d", target, w.Code)
		}
	}
	if w := tgDo(h, "POST", "/telegram/"+itoa(p1), url.Values{"on": {"1"}}, true); w.Code != http.StatusForbidden {
		t.Errorf("POST с чужого ПК: %d", w.Code)
	}
}

// Список: станки по участкам, «Без участка» в конце; период
func TestTGList(t *testing.T) {
	st, h, _, _ := tgServer(t)
	b := tgDo(h, "GET", "/telegram", nil, false).Body.String()
	i, j, k := strings.Index(b, "Мехобработка"), strings.Index(b, "<b>Станок</b>"), strings.Index(b, "Без участка")
	if i < 0 || j < i || k < j || !strings.Contains(b[k:], "Пресс") || !strings.Contains(b, "выключен") {
		t.Fatalf("список станков:\n%s", b)
	}
	if w := tgDo(h, "POST", "/telegram/period", url.Values{"period": {"120"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("период: %d", w.Code)
	}
	if p, _ := st.TGPeriod(context.Background()); p != 120 {
		t.Errorf("период в базе: %d", p)
	}
	if w := tgDo(h, "POST", "/telegram/period", url.Values{"period": {"45"}}, false); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "период") {
		t.Errorf("период 45 с: %d", w.Code)
	}
}

// Станок: сигнал режима предложен из паспорта; сохранение карточки
func TestTGMachineSave(t *testing.T) {
	st, h, p1, sig := tgServer(t)
	page := "/telegram/" + itoa(p1)
	b := tgDo(h, "GET", page, nil, false).Body.String()
	if !strings.Contains(b, `value="`+itoa(sig["Режим"])+`" selected`) || !strings.Contains(b, "предложен из настройки паспорта") {
		t.Fatalf("сигнал режима не предложен из паспорта:\n%s", b)
	}
	// REAL и BOOL — не сигнал режима
	if strings.Contains(b, "Давление (REAL)") {
		t.Error("REAL в списке сигналов режима")
	}
	w := tgDo(h, "POST", page, url.Values{"on": {"on"}, "label": {"Шлифовка"}, "mode": {itoa(sig["Режим"])},
		"show_state": {"on"}, "show_bar": {"on"}, "alarm": {"on"}}, false)
	if w.Code != http.StatusSeeOther || !strings.HasSuffix(w.Header().Get("Location"), "#machine") {
		t.Fatalf("сохранение: %d %s", w.Code, w.Body.String())
	}
	m, _ := st.TGMachine(context.Background(), p1)
	if !m.Enabled || m.Label != "Шлифовка" || m.ModeSignalID != sig["Режим"] || !m.Show.Bar || m.Show.Step || m.OfflineState {
		t.Errorf("в базе: %+v", m)
	}
	if b := tgDo(h, "GET", page, nil, false).Body.String(); strings.Contains(b, "предложен из настройки паспорта") {
		t.Error("после сохранения всё ещё «предложен»")
	}
}

// Состояния: значения строкой, переезд значения, «не входят никуда», по биту, порядок, удаление
func TestTGStatesPage(t *testing.T) {
	st, h, p1, sig := tgServer(t)
	ctx := context.Background()
	page := "/telegram/" + itoa(p1)
	st.SaveTGMachine(ctx, "тест", store.TGMachine{PLCID: p1, ModeSignalID: sig["Режим"]})

	for _, n := range []string{"Автомат", "Ручной"} {
		if w := tgDo(h, "POST", page+"/states", url.Values{"name": {n}, "color": {"blue"}}, false); w.Code != http.StatusSeeOther {
			t.Fatalf("добавить %s: %d %s", n, w.Code, w.Body.String())
		}
	}
	list, _ := st.TGStates(ctx, p1)
	auto, manual := itoa(list[0].ID), itoa(list[1].ID)

	save := func(id string, f url.Values) *httptest.ResponseRecorder {
		return tgDo(h, "POST", page+"/states/"+id, f, false)
	}
	save(auto, url.Values{"name": {"Автомат"}, "color": {"blue"}, "kind": {"code"}, "codes": {"1, 2"}, "pct": {"on"}})
	save(manual, url.Values{"name": {"Ручной"}, "color": {"steel"}, "kind": {"code"}, "codes": {"2"}, "pct": {"on"}})
	list, _ = st.TGStates(ctx, p1)
	if len(list[0].Codes) != 1 || list[0].Codes[0] != 1 || len(list[1].Codes) != 1 || list[1].Codes[0] != 2 || list[1].Color != "steel" {
		t.Fatalf("значение 2 должно было переехать в «Ручной»: %+v", list)
	}
	b := tgDo(h, "GET", page, nil, false).Body.String()
	if !strings.Contains(b, "Не входят ни в одно состояние") || !strings.Contains(b, "3 Наладка") {
		t.Errorf("нет подсказки про значение 3:\n%s", b)
	}
	if !strings.Contains(b, "background:#9AA6B2") {
		t.Error("цвет состояния не дошёл до страницы (ZgotmplZ?)")
	}

	// Ошибки — на той же странице, с причиной
	if w := save(auto, url.Values{"name": {"Автомат"}, "color": {"blue"}, "kind": {"code"}, "codes": {"1, два"}}); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "целые числа") {
		t.Errorf("буквы в значениях: %d", w.Code)
	}
	if w := save(auto, url.Values{"name": {"Автомат"}, "color": {"red"}, "kind": {"code"}}); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "красный") {
		t.Errorf("красный цвет: %d", w.Code)
	}
	if w := save(manual, url.Values{"name": {"Автомат"}, "color": {"blue"}, "kind": {"code"}}); !strings.Contains(w.Body.String(), "уже есть") {
		t.Errorf("повтор названия: %d %s", w.Code, w.Body.String())
	}
	if w := save(manual, url.Values{"name": {"Ручной"}, "color": {"steel"}, "kind": {"bit"}, "bit": {"0"}}); w.Code != http.StatusBadRequest {
		t.Errorf("по биту без бита: %d", w.Code)
	}
	if w := save(manual, url.Values{"name": {"Ручной"}, "color": {"steel"}, "kind": {"bit"}, "bit": {itoa(sig["Давление"])}}); !strings.Contains(w.Body.String(), "BOOL") {
		t.Errorf("по биту REAL: %d", w.Code)
	}

	// По биту — значения снимаются
	save(manual, url.Values{"name": {"Ручной"}, "color": {"steel"}, "kind": {"bit"}, "bit": {itoa(sig["Авто"])}, "codes": {"2"}})
	list, _ = st.TGStates(ctx, p1)
	if list[1].BitSignalID != sig["Авто"] || len(list[1].Codes) != 0 {
		t.Errorf("по биту: %+v", list[1])
	}

	// Порядок и удаление
	tgDo(h, "POST", page+"/states/"+manual+"/move?d=-1", url.Values{}, false)
	if list, _ = st.TGStates(ctx, p1); list[0].Name != "Ручной" {
		t.Errorf("порядок: %+v", list)
	}
	tgDo(h, "POST", page+"/states/"+auto+"/delete", url.Values{}, false)
	if list, _ = st.TGStates(ctx, p1); len(list) != 1 {
		t.Errorf("удаление: %+v", list)
	}
}

// Сигналы карточки: добавить, подпись и показ, порядок, убрать
func TestTGSignalsPage(t *testing.T) {
	st, h, p1, sig := tgServer(t)
	ctx := context.Background()
	page := "/telegram/" + itoa(p1)
	for _, n := range []string{"Давление", "Режим"} {
		if w := tgDo(h, "POST", page+"/signals", url.Values{"sig": {itoa(sig[n])}}, false); w.Code != http.StatusSeeOther {
			t.Fatalf("добавить %s: %d %s", n, w.Code, w.Body.String())
		}
	}
	// Уже добавленные не предлагаются ещё раз
	b := tgDo(h, "GET", page, nil, false).Body.String()
	add := b[strings.Index(b, `name="sig"`):]
	if strings.Contains(add, "Давление ·") || !strings.Contains(add, "Авто ·") {
		t.Errorf("список «+ сигнал»:\n%s", add)
	}
	p := page + "/signals/" + itoa(sig["Давление"])
	if w := tgDo(h, "POST", p, url.Values{"how": {"minmax"}, "label": {"Давление, бар"}}, false); w.Code != http.StatusSeeOther {
		t.Fatalf("правка: %d %s", w.Code, w.Body.String())
	}
	if w := tgDo(h, "POST", p, url.Values{"how": {"chart"}, "label": {"x"}}, false); w.Code != http.StatusBadRequest {
		t.Errorf("неизвестный показ: %d", w.Code)
	}
	tgDo(h, "POST", page+"/signals/"+itoa(sig["Режим"])+"/move?d=-1", url.Values{}, false)
	list, _ := st.TGSignals(ctx, p1)
	if len(list) != 2 || list[0].Name != "Режим" || list[1].Label != "Давление, бар" || list[1].How != "minmax" {
		t.Errorf("после правки и порядка: %+v", list)
	}
	tgDo(h, "POST", p+"/delete", url.Values{}, false)
	if list, _ = st.TGSignals(ctx, p1); len(list) != 1 {
		t.Errorf("после удаления: %+v", list)
	}
}
