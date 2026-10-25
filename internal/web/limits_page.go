package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Страница «Нормы»: границы и скачки аналоговых сигналов ----------
//
// Как на странице «Сигналы»: строка таблицы — сигнал, «изменить» превращает её в форму
// прямо на месте (HTMX), «сохранить» — обратно в строку.

// limitView — строка таблицы норм
type limitView struct {
	Sig   store.SignalRow
	L     store.Limit
	Has   bool   // нормы заданы
	Descr string // нормы одной строкой
	Err   string // ошибка формы
	// Значения полей формы как строки: при ошибке показываем то, что ввёл человек
	Lo, Hi, Hyst, Jump, JumpMs, Kind string
}

func newLimitView(sig store.SignalRow, l store.Limit, has bool) limitView {
	v := limitView{Sig: sig, L: l, Has: has, Descr: "нет", Kind: store.KindWarning, JumpMs: "1000"}
	if has {
		v.Descr = store.DescribeLimit(l, sig.Unit)
		v.Lo, v.Hi, v.Jump = ptrStr(l.Lo), ptrStr(l.Hi), ptrStr(l.Jump)
		v.Hyst = strconv.FormatFloat(l.Hyst, 'f', -1, 64)
		if l.Jump != nil {
			v.JumpMs = strconv.FormatInt(l.JumpMs, 10)
		}
		v.Kind = l.Kind
	}
	return v
}

// ptrStr — число или пусто, если границы нет
func ptrStr(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// limitsPage — GET /limits?plc=1
func (s *Server) limitsPage(w http.ResponseWriter, r *http.Request) {
	pl, err := s.pagePLC(r)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows, err := s.Store.Signals(r.Context(), pl.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lims, err := s.Store.Limits(r.Context(), pl.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var list []limitView
	for _, sig := range rows {
		if !analog(sig) {
			continue
		}
		l, has := lims[sig.ID]
		list = append(list, newLimitView(sig, l, has))
	}
	render(w, "limits", map[string]any{"PLC": pl, "PLCs": all, "List": list})
}

// analog — нормы бывают только у аналоговых трендов (не у битов и не у аварий)
func analog(sig store.SignalRow) bool {
	return sig.Kind == store.KindTrend && sig.Type != "BOOL"
}

// limitRowFromPath — сигнал по {id} и его нормы; при ошибке сам отвечает клиенту
func (s *Server) limitRowFromPath(w http.ResponseWriter, r *http.Request) (limitView, bool) {
	sig, ok := s.rowFromPath(w, r)
	if !ok {
		return limitView{}, false
	}
	if !analog(sig) {
		http.Error(w, "нормы — только для аналоговых сигналов", http.StatusBadRequest)
		return limitView{}, false
	}
	lims, err := s.Store.Limits(r.Context(), sig.PLCID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return limitView{}, false
	}
	l, has := lims[sig.ID]
	return newLimitView(sig, l, has), true
}

// limitRow — GET /ui/limits/{id}: строка (и «отмена» в форме)
func (s *Server) limitRow(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.limitRowFromPath(w, r); ok {
		render(w, "limitrow", v)
	}
}

// limitEdit — GET /ui/limits/{id}/edit: строка-форма
func (s *Server) limitEdit(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.limitRowFromPath(w, r); ok {
		render(w, "limitedit", v)
	}
}

// limitSave — PUT /ui/limits/{id}: сохранить нормы (все поля пустые — снять)
func (s *Server) limitSave(w http.ResponseWriter, r *http.Request) {
	v, ok := s.limitRowFromPath(w, r)
	if !ok {
		return
	}
	r.ParseForm()
	f := r.PostForm
	v.Lo, v.Hi, v.Hyst = f.Get("lo"), f.Get("hi"), f.Get("hyst")
	v.Jump, v.JumpMs, v.Kind = f.Get("jump"), f.Get("jump_ms"), f.Get("kind")

	l, err := limitFromForm(v)
	if err == nil {
		err = s.Store.SetLimit(r.Context(), who(r), v.Sig, l)
	}
	if err != nil {
		v.Err = err.Error()
		render(w, "limitedit", v)
		return
	}
	render(w, "limitrow", newLimitView(v.Sig, l, !l.Empty()))
}

// limitFromForm — поля формы → нормы. Пустое поле — «границы нет».
func limitFromForm(v limitView) (store.Limit, error) {
	l := store.Limit{Kind: v.Kind}
	var err error
	if l.Lo, err = optNum(v.Lo); err != nil {
		return l, errors.New("нижняя граница: число")
	}
	if l.Hi, err = optNum(v.Hi); err != nil {
		return l, errors.New("верхняя граница: число")
	}
	if l.Hyst, err = parseNum(v.Hyst, 0); err != nil {
		return l, errors.New("зона возврата: число")
	}
	if l.Jump, err = optNum(v.Jump); err != nil {
		return l, errors.New("скачок: число")
	}
	if l.Jump != nil {
		if l.JumpMs, err = strconv.ParseInt(strings.TrimSpace(v.JumpMs), 10, 64); err != nil {
			return l, errors.New("время скачка: целое число миллисекунд")
		}
	}
	if !l.Empty() { // пустые нормы проверять незачем — их просто снимут
		err = l.Check()
	}
	return l, err
}

// optNum — число или nil для пустого поля. Точка и запятая — как в parseNum.
func optNum(s string) (*float64, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	x, err := parseNum(s, 0)
	if err != nil {
		return nil, err
	}
	return &x, nil
}
