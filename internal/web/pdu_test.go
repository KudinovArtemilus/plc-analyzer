package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// pressRows — буфер пресса: 40 клапанов по 12 байт = 480 байт подряд, все 100 мс
func pressRows() []store.SignalRow {
	var rows []store.SignalRow
	for i := range 40 {
		rows = append(rows, row(fmt.Sprintf("Y%d.Давление", i+1), fmt.Sprintf("DB120.DBD%d", i*12+2), "REAL", 100))
		rows = append(rows, row(fmt.Sprintf("Y%d.Код", i+1), fmt.Sprintf("DB120.DBW%d", i*12+10), "INT", 100))
	}
	return rows
}

func TestPollPlanPDU(t *testing.T) {
	// PDU 240: по 200 байт → 3 запроса; PDU 480: по 440 → 2
	for pdu, want := range map[int]int{240: 3, 480: 2, 0: 3} {
		v := pollPlan(pressRows(), pdu)
		if v.Requests != want {
			t.Errorf("PDU %d: запросов %d, ждали %d", pdu, v.Requests, want)
		}
	}
	if v := pollPlan(pressRows(), 480); v.MaxSize != 440 || v.PDU != 480 ||
		!strings.Contains(v.Groups[0].Blocks[1].Why, "больше 440 байт") {
		t.Errorf("PDU 480: %+v", v)
	}
}

func TestPDUHint(t *testing.T) {
	cases := []struct {
		set, got int
		want     string // кусок подсказки; "" — подсказки нет
	}{
		{240, 0, ""},   // ещё не подключались
		{240, 240, ""}, // сходится
		{480, 960, ""}, // gos7 больше 480 не возьмёт — тоже сходится
		{240, 480, "Можно поставить 480"}, // S7-1500 с настройкой 240
		{480, 240, "Поставьте PDU 240"},   // S7-300 с настройкой 480
		{0, 480, "указано 240"},           // не указан — считаем 240
	}
	for _, c := range cases {
		h := pduHint(c.set, c.got)
		if (c.want == "") != (h == "") || !strings.Contains(h, c.want) {
			t.Errorf("pduHint(%d, %d) = %q, ждали %q", c.set, c.got, h, c.want)
		}
	}
}

// Через сервер: форма ПЛК сохраняет PDU, «Сигналы» строят план по нему и показывают подсказку
func TestPDUPages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.1", Slot: 1})
	if err := st.AddSignals(ctx, "тест", pressRowsFor(id)); err != nil {
		t.Fatal(err)
	}
	pl, _ := st.PLC(ctx, id)
	reported := 480 // столько ПЛК «дал» при подключении
	srv := &Server{Store: st, PLC: pl, Units: func(int64) (plc.Status, archive.Status, bool) {
		return plc.Status{Connected: true, PDU: reported}, archive.Status{}, true
	}}
	h := srv.Handler()
	do := func(method, target string, form url.Values) *httptest.ResponseRecorder {
		var r *http.Request
		if form != nil {
			r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		r.RemoteAddr = "127.0.0.1:5000" // ПК анализатора — менять можно
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	sigs := fmt.Sprintf("/signals?plc=%d", id)

	// В настройке 240, ПЛК дал 480 → 3 запроса и подсказка «можно 480»
	page := do("GET", sigs, nil).Body.String()
	for _, want := range []string{"→ 3 запроса", "Можно поставить 480", "не больше 200 байт", "PDU 240"} {
		if !strings.Contains(page, want) {
			t.Errorf("«Сигналы» при PDU 240: нет %q", want)
		}
	}
	// Страница «ПЛК»: столбец и выбор PDU, состояние — что дал ПЛК
	if p := do("GET", "/plcs", nil).Body.String(); !strings.Contains(p, `<option value="480"`) || !strings.Contains(p, "<td>240</td>") {
		t.Error("«ПЛК»: нет выбора или столбца PDU")
	}
	if p := do("GET", fmt.Sprintf("/ui/plcs/%d/state", id), nil).Body.String(); !strings.Contains(p, "ПЛК дал PDU 480") {
		t.Errorf("состояние ПЛК: %s", p)
	}

	// Сохранили 480 через форму → 2 запроса, подсказки нет
	form := url.Values{"name": {"Пресс"}, "ip": {"192.168.0.1"}, "rack": {"0"}, "slot": {"1"}, "pdu": {"480"}}
	if w := do("PUT", fmt.Sprintf("/ui/plcs/%d", id), form); !strings.Contains(w.Body.String(), "<td>480</td>") {
		t.Fatalf("сохранение PDU 480: %d %s", w.Code, w.Body.String())
	}
	if p, _ := st.PLC(ctx, id); p.PDU != 480 {
		t.Fatalf("в базе PDU %d", p.PDU)
	}
	page = do("GET", sigs, nil).Body.String()
	if !strings.Contains(page, "→ 2 запроса") || strings.Contains(page, "Можно поставить") {
		t.Error("«Сигналы» при PDU 480: ждали 2 запроса без подсказки")
	}

	// 960 нельзя: gos7 больше 480 не просит
	form.Set("pdu", "960")
	if w := do("PUT", fmt.Sprintf("/ui/plcs/%d", id), form); !strings.Contains(w.Body.String(), "PDU — 240") {
		t.Errorf("PDU 960 должен быть отклонён: %s", w.Body.String())
	}
}

func pressRowsFor(plcID int64) []store.SignalRow {
	rows := pressRows()
	for i := range rows {
		rows[i].PLCID = plcID
		rows[i].Scale = 1
	}
	return rows
}
