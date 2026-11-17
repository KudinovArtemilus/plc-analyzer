package web

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Страница «Telegram» (шаг 45a-3) ----------
//
// Что будет видно на телефоне: станки по участкам, у станка — свои состояния, карточка и сигналы.
// Наружу пока не уходит НИЧЕГО: это только настройки в базе (отправка — шаг 45b).
//
// Устроено как «Наборы»: обычные формы, POST → redirect → GET, без HTMX. У каждой строки
// (состояние, сигнал) — своя маленькая форма: сохранили одну строку — остальные не трогаются.
//
// Страница открывается ТОЛЬКО на ПК анализатора (и с адресов -admin): здесь решается,
// что уйдёт с завода, — чужим ПК её и смотреть незачем. Изменения и так режет guard (access.go).

// tgListView — первая страница: период и станки по участкам
type tgListView struct {
	Period int
	Areas  []tgAreaView
	Err    string
}

type tgAreaView struct {
	Name     string
	Machines []tgListRow
}

// tgListRow — строка станка в списке: сколько у него состояний и сигналов
type tgListRow struct {
	PLC     store.PLC
	M       store.TGMachine
	States  int
	Signals int
}

// tgMachineView — страница одного станка
type tgMachineView struct {
	PLC           store.PLC
	M             store.TGMachine
	ModeSuggested bool              // сигнал режима подставлен из паспорта, ещё не сохранён
	ModeSigs      []store.SignalRow // целые сигналы — кандидаты в «сигнал режима»
	Bits          []store.SignalRow // BOOL — для состояний «по биту»
	States        []tgStateView
	Codes         []store.DictItem // значения сигнала режима из его справочника
	Unused        []store.DictItem // значения, которые не входят ни в одно состояние
	Signals       []store.TGSignal // сигналы карточки
	Free          []store.SignalRow
	Err           string
}

// tgStateView — состояние и его значения строкой «1, 2» для поля ввода
type tgStateView struct {
	store.TGState
	CodesText string
}

// Названия для экрана — в Go, а не в шаблоне: шаблону остаётся {{tgColorName .Color}}
var tgColorNames = map[string]string{
	"blue": "тёмно-синий", "steel": "серо-голубой", "green": "зелёный", "orange": "оранжевый", "purple": "фиолетовый",
}

// tgColorHex — те же цвета, что будут на телефоне (макет Mini App)
var tgColorHex = map[string]string{
	"blue": "#2F4A68", "steel": "#9AA6B2", "green": "#3F7D4E", "orange": "#C2671B", "purple": "#6B4E9B",
}

var tgHowNames = map[string]string{
	"value": "текущее", "minmax": "текущее + мин/макс", "dict": "с подписью из справочника",
}

func tgPeriodName(sec int) string {
	switch sec {
	case 60:
		return "раз в минуту"
	case 300:
		return "раз в 5 мин"
	default:
		return "каждые " + strconv.Itoa(sec) + " с"
	}
}

// isIntType — может ли сигнал быть «сигналом режима»: только целые числа
func isIntType(t string) bool {
	switch strings.ToUpper(t) {
	case "BYTE", "INT", "WORD", "DINT", "DWORD":
		return true
	}
	return false
}

// tgOnlyHere — страница только для ПК анализатора; чужому ПК — 403 с объяснением
func (s *Server) tgOnlyHere(w http.ResponseWriter, r *http.Request) bool {
	if s.canEdit(r) {
		return true
	}
	http.Error(w, "Настройка Telegram открывается только на ПК анализатора: здесь решается, что уйдёт с завода.",
		http.StatusForbidden)
	return false
}

// ---------- Список станков ----------

// tgPage — GET /telegram
func (s *Server) tgPage(w http.ResponseWriter, r *http.Request) {
	if !s.tgOnlyHere(w, r) {
		return
	}
	v, err := s.tgList(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "telegram", v)
}

// tgList — участки по порядку, в конце — «Без участка» (если там кто-то есть)
func (s *Server) tgList(r *http.Request) (tgListView, error) {
	ctx := r.Context()
	var v tgListView
	var err error
	if v.Period, err = s.Store.TGPeriod(ctx); err != nil {
		return v, err
	}
	areas, err := s.Store.Areas(ctx)
	if err != nil {
		return v, err
	}
	plcs, err := s.Store.PLCs(ctx)
	if err != nil {
		return v, err
	}
	idx := map[int64]int{}
	for _, a := range areas {
		idx[a.ID] = len(v.Areas)
		v.Areas = append(v.Areas, tgAreaView{Name: a.Name})
	}
	none := tgAreaView{Name: "Без участка"}
	for _, pl := range plcs {
		row := tgListRow{PLC: pl}
		if row.M, err = s.Store.TGMachine(ctx, pl.ID); err != nil {
			return v, err
		}
		states, err := s.Store.TGStates(ctx, pl.ID)
		if err != nil {
			return v, err
		}
		sigs, err := s.Store.TGSignals(ctx, pl.ID)
		if err != nil {
			return v, err
		}
		row.States, row.Signals = len(states), len(sigs)
		if i, ok := idx[pl.AreaID]; ok {
			v.Areas[i].Machines = append(v.Areas[i].Machines, row)
		} else {
			none.Machines = append(none.Machines, row)
		}
	}
	if len(none.Machines) > 0 {
		v.Areas = append(v.Areas, none)
	}
	return v, nil
}

// tgPeriodSave — POST /telegram/period
func (s *Server) tgPeriodSave(w http.ResponseWriter, r *http.Request) {
	sec, _ := strconv.Atoi(r.FormValue("period"))
	if err := s.Store.SetTGPeriod(r.Context(), who(r), sec); err != nil {
		v, lerr := s.tgList(r)
		if lerr != nil {
			http.Error(w, lerr.Error(), http.StatusInternalServerError)
			return
		}
		v.Err = err.Error()
		w.WriteHeader(http.StatusBadRequest)
		render(w, "telegram", v)
		return
	}
	http.Redirect(w, r, "/telegram", http.StatusSeeOther)
}

// ---------- Один станок ----------

// tgMachinePage — GET /telegram/{plc}
func (s *Server) tgMachinePage(w http.ResponseWriter, r *http.Request) {
	if !s.tgOnlyHere(w, r) {
		return
	}
	v, ok := s.tgMachine(w, r)
	if !ok {
		return
	}
	render(w, "tgmachine", v)
}

// tgMachine — всё для страницы станка; ПЛК нет — сам отвечает 404
func (s *Server) tgMachine(w http.ResponseWriter, r *http.Request) (tgMachineView, bool) {
	ctx := r.Context()
	var v tgMachineView
	id, _ := strconv.ParseInt(r.PathValue("plc"), 10, 64)
	pl, err := s.Store.PLC(ctx, id)
	if err != nil {
		http.Error(w, "ПЛК не найден — возможно, его удалили", http.StatusNotFound)
		return v, false
	}
	v.PLC = pl
	fail := func(err error) (tgMachineView, bool) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return v, false
	}
	if v.M, err = s.Store.TGMachine(ctx, pl.ID); err != nil {
		return fail(err)
	}
	all, err := s.Store.Signals(ctx, pl.ID)
	if err != nil {
		return fail(err)
	}
	states, err := s.Store.TGStates(ctx, pl.ID)
	if err != nil {
		return fail(err)
	}
	if v.Signals, err = s.Store.TGSignals(ctx, pl.ID); err != nil {
		return fail(err)
	}

	// Сигнал режима не выбран — предложить тот, что выбран для паспорта циклов
	if v.M.ModeSignalID == 0 {
		if p, err := s.Store.Passport(ctx, pl.ID); err == nil && p.Mode != "" {
			for _, sig := range all {
				if sig.Name == p.Mode {
					v.M.ModeSignalID, v.ModeSuggested = sig.ID, true
				}
			}
		}
	}

	onCard := map[int64]bool{}
	for _, g := range v.Signals {
		onCard[g.SignalID] = true
	}
	var mode store.SignalRow
	for _, sig := range all {
		switch {
		case isIntType(sig.Type):
			v.ModeSigs = append(v.ModeSigs, sig)
		case strings.EqualFold(sig.Type, "BOOL"):
			v.Bits = append(v.Bits, sig)
		}
		if sig.ID == v.M.ModeSignalID {
			mode = sig
		}
		if !onCard[sig.ID] {
			v.Free = append(v.Free, sig)
		}
	}

	// Значения режима — из справочника сигнала режима; «не входят никуда» — подсказка
	used := map[int64]bool{}
	for _, st := range states {
		v.States = append(v.States, tgStateView{TGState: st, CodesText: codesText(st.Codes)})
		for _, c := range st.Codes {
			used[c] = true
		}
	}
	if mode.DictID != 0 {
		v.Codes, _ = s.Store.DictItems(ctx, mode.DictID)
		for _, it := range v.Codes {
			if !used[it.Value] {
				v.Unused = append(v.Unused, it)
			}
		}
	}
	return v, true
}

// tgDone — после действия: удалось — обратно на страницу станка (к нужному месту),
// нет — та же страница с причиной сверху
func (s *Server) tgDone(w http.ResponseWriter, r *http.Request, err error, anchor string) {
	if err == nil {
		http.Redirect(w, r, "/telegram/"+r.PathValue("plc")+"#"+anchor, http.StatusSeeOther)
		return
	}
	v, ok := s.tgMachine(w, r)
	if !ok {
		return
	}
	v.Err = tgFriendly(err).Error()
	w.WriteHeader(http.StatusBadRequest)
	render(w, "tgmachine", v)
}

// tgFriendly — ошибки базы понятными словами
func tgFriendly(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errors.New("не найдено — возможно, уже удалили")
	case strings.Contains(err.Error(), "UNIQUE"):
		return errors.New("уже есть: состояние с таким названием или этот сигнал на карточке")
	}
	return err
}

func (s *Server) plcID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("plc"), 10, 64)
	return id
}

func pathID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}

func formID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.FormValue(name), 10, 64)
	return id
}

// checked — галочка: отмеченная приходит в форме, снятая не приходит вовсе
func checked(r *http.Request, name string) bool { return r.FormValue(name) != "" }

// tgMachineSave — POST /telegram/{plc}: показывать, подпись, сигнал режима, карточка
func (s *Server) tgMachineSave(w http.ResponseWriter, r *http.Request) {
	m := store.TGMachine{
		PLCID:        s.plcID(r),
		Enabled:      checked(r, "on"),
		Label:        r.FormValue("label"),
		ModeSignalID: formID(r, "mode"),
		Show: store.TGShow{State: checked(r, "show_state"), Step: checked(r, "show_step"),
			Bar: checked(r, "show_bar"), Signals: checked(r, "show_signals"), Alarms: checked(r, "show_alarms")},
		AlarmState:   checked(r, "alarm"),
		OfflineState: checked(r, "offline"),
	}
	s.tgDone(w, r, s.Store.SaveTGMachine(r.Context(), who(r), m), "machine")
}

// ---------- Состояния ----------

// tgStateAdd — POST /telegram/{plc}/states: новое состояние (по коду, без значений)
func (s *Server) tgStateAdd(w http.ResponseWriter, r *http.Request) {
	_, err := s.Store.AddTGState(r.Context(), who(r), store.TGState{
		PLCID: s.plcID(r), Name: r.FormValue("name"), Color: r.FormValue("color"), Pct: true})
	s.tgDone(w, r, err, "states")
}

// tgStateSave — POST /telegram/{plc}/states/{id}: название, цвет, «в %», вид и значения
func (s *Server) tgStateSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := store.TGState{ID: pathID(r, "id"), PLCID: s.plcID(r), Name: r.FormValue("name"),
		Color: r.FormValue("color"), Pct: checked(r, "pct")}
	byBit := r.FormValue("kind") == "bit"
	if byBit {
		st.BitSignalID = formID(r, "bit")
		if st.BitSignalID == 0 {
			s.tgDone(w, r, errors.New("для состояния «по биту» выберите сигнал BOOL"), "states")
			return
		}
	}
	var codes []int64
	if !byBit {
		var err error
		if codes, err = parseCodes(r.FormValue("codes")); err != nil {
			s.tgDone(w, r, err, "states")
			return
		}
	}
	if err := s.Store.UpdateTGState(ctx, who(r), st); err != nil {
		s.tgDone(w, r, err, "states")
		return
	}
	if !byBit {
		if err := s.setCodes(r, st, codes); err != nil {
			s.tgDone(w, r, err, "states")
			return
		}
	}
	s.tgDone(w, r, nil, "states")
}

// setCodes — привести значения состояния к списку: новых — добавить (у другого состояния
// они переедут сюда), лишние — убрать. Не менявшиеся не трогаются — журнал без шума.
func (s *Server) setCodes(r *http.Request, st store.TGState, want []int64) error {
	list, err := s.Store.TGStates(r.Context(), st.PLCID)
	if err != nil {
		return err
	}
	var have []int64
	for _, x := range list {
		if x.ID == st.ID {
			have = x.Codes
		}
	}
	for _, c := range want {
		if !slices.Contains(have, c) {
			if err := s.Store.SetTGStateCode(r.Context(), who(r), st.PLCID, st.ID, c, true); err != nil {
				return err
			}
		}
	}
	for _, c := range have {
		if !slices.Contains(want, c) {
			if err := s.Store.SetTGStateCode(r.Context(), who(r), st.PLCID, st.ID, c, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseCodes — «1, 2 3;4» → [1 2 3 4]; повторы убираются
func parseCodes(text string) ([]int64, error) {
	var out []int64
	for _, f := range strings.FieldsFunc(text, func(c rune) bool { return c == ',' || c == ';' || c == ' ' }) {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, errors.New("значения режима — целые числа через запятую, например: 1, 2")
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func codesText(codes []int64) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = strconv.FormatInt(c, 10)
	}
	return strings.Join(parts, ", ")
}

// tgStateMove — POST /telegram/{plc}/states/{id}/move?d=-1|1
func (s *Server) tgStateMove(w http.ResponseWriter, r *http.Request) {
	d, _ := strconv.Atoi(r.URL.Query().Get("d"))
	s.tgDone(w, r, s.Store.MoveTGState(r.Context(), who(r), s.plcID(r), pathID(r, "id"), d), "states")
}

// tgStateDelete — POST /telegram/{plc}/states/{id}/delete
func (s *Server) tgStateDelete(w http.ResponseWriter, r *http.Request) {
	s.tgDone(w, r, s.Store.DeleteTGState(r.Context(), who(r), s.plcID(r), pathID(r, "id")), "states")
}

// ---------- Сигналы карточки ----------

// tgSignalAdd — POST /telegram/{plc}/signals
func (s *Server) tgSignalAdd(w http.ResponseWriter, r *http.Request) {
	s.tgDone(w, r, s.Store.AddTGSignal(r.Context(), who(r), s.plcID(r), formID(r, "sig")), "signals")
}

// tgSignalSave — POST /telegram/{plc}/signals/{sid}: как показывать и подпись
func (s *Server) tgSignalSave(w http.ResponseWriter, r *http.Request) {
	g := store.TGSignal{PLCID: s.plcID(r), SignalID: pathID(r, "sid"), How: r.FormValue("how"), Label: r.FormValue("label")}
	s.tgDone(w, r, s.Store.UpdateTGSignal(r.Context(), who(r), g), "signals")
}

// tgSignalMove — POST /telegram/{plc}/signals/{sid}/move?d=-1|1
func (s *Server) tgSignalMove(w http.ResponseWriter, r *http.Request) {
	d, _ := strconv.Atoi(r.URL.Query().Get("d"))
	s.tgDone(w, r, s.Store.MoveTGSignal(r.Context(), who(r), s.plcID(r), pathID(r, "sid"), d), "signals")
}

// tgSignalDelete — POST /telegram/{plc}/signals/{sid}/delete
func (s *Server) tgSignalDelete(w http.ResponseWriter, r *http.Request) {
	s.tgDone(w, r, s.Store.DeleteTGSignal(r.Context(), who(r), s.plcID(r), pathID(r, "sid")), "signals")
}
