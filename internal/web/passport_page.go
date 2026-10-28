package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Паспорт циклов: блок на странице «Шаги» и сохранение настройки (34a-2) ----------
//
// На странице «Шаги» человек подбирает, как искать циклы: сигнал шага, режим «автомат»,
// исходный шаг — и сразу видит результат. Кнопка «Сохранить для паспорта» запоминает
// этот выбор в базе, и дальше служба считает паспорт по каждой смене сама (34a-3).

// passportView — блок «Паспорт циклов»
type passportView struct {
	Saved    string // что сохранено сейчас, словами; "" — паспорт для ПЛК не настроен
	Same     bool   // на странице выбрано ровно то, что сохранено
	ShowSave bool   // показать кнопку: циклы на странице есть, и выбрано не то, что сохранено
	Home     int64  // исходный шаг, выбранный на странице, — уходит в форму
	ModeVal  string // значение режима для формы; "" — режим не учитывается
}

// fillPassport — блок паспорта на странице «Шаги». Вызывается, когда журнал уже посчитан.
func (s *Server) fillPassport(ctx context.Context, v *stepsView, l dictLookup) {
	p := &v.Passport
	home, ok := selectedHome(v.Cycles.Home)
	if !ok {
		return // шагов нет — и сохранять нечего
	}
	p.Home = home

	// Режим учитывается, только если выбрано и значение: так же считает и сама страница
	mode := ""
	if _, err := strconv.ParseInt(v.ModeVal, 10, 64); err == nil && v.Mode != "" {
		mode, p.ModeVal = v.Mode, v.ModeVal
	}

	saved, err := s.Store.Passport(ctx, v.PLC)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// не настроен — Saved пустой
	case err != nil:
		p.Saved = "не удалось прочитать из базы: " + err.Error()
	default:
		p.Saved = describePassport(saved, l)
		p.Same = saved.Step == v.Sig && saved.Home == home && saved.Mode == mode &&
			(mode == "" || strconv.FormatInt(saved.ModeVal, 10) == p.ModeVal)
	}
	p.ShowSave = v.Cycles.Count > 0 && !p.Same
}

// selectedHome — исходный шаг, выбранный (или предложенный) в разделе «Циклы»
func selectedHome(opts []valOption) (int64, bool) {
	for _, o := range opts {
		if o.Selected {
			return o.Value, true
		}
	}
	return 0, false
}

// describePassport: «шаг «Шаги», исходный 0 · Базовая позиция, режим «Режим» = 1 · Автомат»
func describePassport(p store.Passport, l dictLookup) string {
	stepText, _ := l.text(p.Step, p.Home)
	s := "шаг «" + p.Step + "», исходный " + withText(p.Home, stepText)
	if p.Mode != "" {
		modeText, _ := l.text(p.Mode, p.ModeVal)
		s += ", режим «" + p.Mode + "» = " + withText(p.ModeVal, modeText)
	}
	return s
}

// savePassport — POST /passport: сохранить настройку и вернуться на ту же страницу «Шаги»
func (s *Server) savePassport(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.PostForm
	plc, err := strconv.ParseInt(f.Get("plc"), 10, 64)
	if err != nil {
		http.Error(w, "не указан ПЛК", http.StatusBadRequest)
		return
	}
	home, err := strconv.ParseInt(f.Get("home"), 10, 64)
	if err != nil {
		http.Error(w, "исходный шаг — целое число", http.StatusBadRequest)
		return
	}
	p := store.Passport{PLCID: plc, Step: f.Get("sig"), Home: home}
	if mv, err := strconv.ParseInt(f.Get("mv"), 10, 64); err == nil && f.Get("mode") != "" {
		p.Mode, p.ModeVal = f.Get("mode"), mv
	}
	if err := s.Store.SavePassport(r.Context(), who(r), p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	back := url.Values{}
	for _, k := range []string{"plc", "file", "sig", "min", "mode", "mv", "home"} {
		if x := f.Get(k); x != "" {
			back.Set(k, x)
		}
	}
	http.Redirect(w, r, "/steps?"+back.Encode(), http.StatusSeeOther)
}
