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

// Сохранение настройки через настоящий сервер и базу, затем блок на странице «Шаги»
func TestPassportSave(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plcID, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.1", Slot: 2})
	pl, _ := st.PLC(ctx, plcID)
	s := &Server{Store: st, PLC: pl}
	h := s.Handler()

	post := func(from string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/passport", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = from
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	form := url.Values{"plc": {itoa(plcID)}, "file": {"2026/10/28/A_1.pla"}, "sig": {"Шаги"},
		"mode": {"Режим"}, "mv": {"1"}, "home": {"0"}}

	// С чужого ПК менять нельзя — настройка не появилась
	post("10.0.0.99:5000", form)
	if _, err := st.Passport(ctx, plcID); err == nil {
		t.Fatal("чужой ПК сохранил настройку паспорта")
	}

	// Со своего — сохранилось, и возвращаемся на ту же страницу «Шаги»
	w := post("127.0.0.1:5000", form)
	loc := w.Header().Get("Location")
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/steps?") || !strings.Contains(loc, "home=0") {
		t.Fatalf("код %d, Location %q, тело %s", w.Code, loc, w.Body.String())
	}
	p, err := st.Passport(ctx, plcID)
	if err != nil || p.Step != "Шаги" || p.Mode != "Режим" || p.ModeVal != 1 || p.Home != 0 {
		t.Fatalf("в базе %+v, %v", p, err)
	}

	// Режим без значения — режим не учитывается (так же считает и страница)
	form.Set("mv", "")
	post("127.0.0.1:5000", form)
	if p, _ := st.Passport(ctx, plcID); p.Mode != "" {
		t.Errorf("режим без значения сохранился: %+v", p)
	}
	form.Set("mv", "1")
	post("127.0.0.1:5000", form)

	// Блок на странице: выбрано то же, что сохранено, — кнопки нет
	v := stepsView{PLC: plcID, File: "f.pla", Sig: "Шаги", Mode: "Режим", ModeVal: "1"}
	v.Cycles.Count = 5
	v.Cycles.Home = []valOption{{Value: 0, Selected: true}, {Value: 10}}
	s.fillPassport(ctx, &v, dictLookup{})
	if !v.Passport.Same || v.Passport.ShowSave || !strings.Contains(v.Passport.Saved, "режим «Режим» = 1") {
		t.Errorf("то же самое: %+v", v.Passport)
	}

	// Выбран другой исходный шаг — предложить сохранить
	v.Cycles.Home = []valOption{{Value: 0}, {Value: 10, Selected: true}}
	v.Passport = passportView{}
	s.fillPassport(ctx, &v, dictLookup{})
	if v.Passport.Same || !v.Passport.ShowSave || v.Passport.Home != 10 {
		t.Errorf("другой исходный шаг: %+v", v.Passport)
	}

	v.Summary = []stepStatView{{Value: 0}} // раздел «Циклы» и паспорт — внутри {{if .Summary}}
	rec := httptest.NewRecorder()
	render(rec, "steps", v)
	b := strings.Join(strings.Fields(rec.Body.String()), " ")
	for _, want := range []string{"Паспорт циклов", `action="/passport"`, `name="home" value="10"`,
		`name="mv" value="1"`, "Сохранить эти настройки для паспорта", "</html>"} {
		if !strings.Contains(b, want) {
			t.Errorf("страница: нет %q", want)
		}
	}
}
