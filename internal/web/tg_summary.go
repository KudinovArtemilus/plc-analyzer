package web

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/steps"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
)

// ---------- Сводка для телефона (шаг 45a-4) ----------
//
// Собирает summary.Summary из того, что анализатор и так знает: состояние связи, буфер Live
// (значения «сейчас» и мин/макс за период), журнал аварий и файлы текущей смены (доли в %).
// Пока сводка только показывается на странице «Telegram» («Что уйдёт наружу»); отправка — 45b.
//
// Наружу попадает только то, что описано типами пакета summary: подписи, значения, проценты.
// IP, адреса сигналов, пути к файлам сюда не передаются вовсе — тест это проверяет.

// TGSummary — сводка для бота Telegram (45b): то же, что «Что уйдёт наружу»
func (s *Server) TGSummary(ctx context.Context, now time.Time) (summary.Summary, error) {
	return s.tgSummary(ctx, now)
}

// tgSummary — сводка по всем станкам, у которых стоит «показывать»
func (s *Server) tgSummary(ctx context.Context, now time.Time) (summary.Summary, error) {
	out := summary.Summary{At: now.Format(time.RFC3339), Areas: []summary.Area{}}
	var err error
	if out.Period, err = s.Store.TGPeriod(ctx); err != nil {
		return out, err
	}
	areas, err := s.Store.Areas(ctx)
	if err != nil {
		return out, err
	}
	plcs, err := s.Store.PLCs(ctx)
	if err != nil {
		return out, err
	}
	byArea := map[int64][]summary.Machine{}
	for _, pl := range plcs {
		m, err := s.Store.TGMachine(ctx, pl.ID)
		if err != nil {
			return out, err
		}
		if !m.Enabled {
			continue // не отмечен — о нём наружу не уходит ничего
		}
		mc, err := s.tgMachineSummary(ctx, pl, m, now)
		if err != nil {
			return out, err
		}
		byArea[pl.AreaID] = append(byArea[pl.AreaID], mc)
	}
	for _, a := range areas {
		if ms := byArea[a.ID]; len(ms) > 0 {
			out.Areas = append(out.Areas, summary.Area{Name: a.Name, Machines: ms})
			delete(byArea, a.ID)
		}
	}
	var rest []summary.Machine // без участка (или участок только что удалили)
	for _, ms := range byArea {
		rest = append(rest, ms...)
	}
	if len(rest) > 0 {
		sort.Slice(rest, func(a, b int) bool { return rest[a].Name < rest[b].Name })
		out.Areas = append(out.Areas, summary.Area{Name: "Без участка", Machines: rest})
	}
	return out, nil
}

// tgRules — состояния станка для расчёта: имя сигнала режима и правила (свои состояния,
// «Авария», «Нет связи»). Общие для карточки «сейчас» и итога смены (45e).
func (s *Server) tgRules(ctx context.Context, plcID int64, m store.TGMachine) (string, summary.Rules, error) {
	rules := summary.Rules{Alarm: m.AlarmState, Offline: m.OfflineState}
	states, err := s.Store.TGStates(ctx, plcID)
	if err != nil {
		return "", rules, err
	}
	all, err := s.Store.Signals(ctx, plcID)
	if err != nil {
		return "", rules, err
	}
	nameOf := map[int64]string{}
	for _, sg := range all {
		nameOf[sg.ID] = sg.Name
	}
	for _, st := range states {
		rules.States = append(rules.States, summary.Rule{Name: st.Name, Color: st.Color, Pct: st.Pct,
			Codes: st.Codes, Bit: nameOf[st.BitSignalID]})
	}
	return nameOf[m.ModeSignalID], rules, nil
}

// tgMachineSummary — карточка одного станка
func (s *Server) tgMachineSummary(ctx context.Context, pl store.PLC, m store.TGMachine, now time.Time) (summary.Machine, error) {
	mc := summary.Machine{Name: pl.Name}
	if m.Label != "" {
		mc.Name = m.Label
	}
	mode, rules, err := s.tgRules(ctx, pl.ID, m)
	if err != nil {
		return mc, err
	}

	// Сейчас: связь, аварии, режим и биты — из буфера Live
	live := s.liveValues(pl.ID, now)
	if s.Units != nil {
		st, _, ok := s.Units(pl.ID)
		mc.Online = ok && st.Connected
	}
	alarms, err := s.tgActiveAlarms(ctx, pl.ID, now)
	if err != nil {
		return mc, err
	}
	mom := summary.Moment{Online: mc.Online, Alarm: len(alarms) > 0, Bits: map[string]bool{}}
	if v, ok := live.last(mode); ok {
		mom.Mode, mom.HasMode = int64(math.Round(v)), true
	}
	for _, r := range rules.States {
		if v, ok := live.last(r.Bit); ok && r.Bit != "" {
			mom.Bits[r.Bit] = v == 1
		}
	}
	l := s.lookup(ctx, pl.ID)
	if m.Show.State {
		mc.State = summary.StateOf(rules, summary.Classify(rules, mom))
	}
	if m.Show.Step {
		if p, err := s.Store.Passport(ctx, pl.ID); err == nil && p.Step != "" {
			if v, ok := live.last(p.Step); ok {
				mc.Step = numText(l, p.Step, v)
			}
		}
	}
	if m.Show.Bar {
		sh, err := s.tgShift(ctx, pl, mode, rules, now)
		if err != nil {
			return mc, err
		}
		mc.Shift = sh
	}
	if m.Show.Signals {
		sigs, err := s.Store.TGSignals(ctx, pl.ID)
		if err != nil {
			return mc, err
		}
		period, err := s.Store.TGPeriod(ctx)
		if err != nil {
			return mc, err
		}
		since := now.Add(-time.Duration(period) * time.Second) // мин/макс — за период отправки
		for _, g := range sigs {
			mc.Signals = append(mc.Signals, tgSignalOf(g, live, l, since))
		}
	}
	if m.Show.Alarms {
		mc.Alarms = alarms
	}
	return mc, nil
}

// tgSignalOf — сигнал карточки: значение (и мин/макс за период) или текст из справочника
func tgSignalOf(g store.TGSignal, live liveData, l dictLookup, since time.Time) summary.Signal {
	out := summary.Signal{Label: g.Label}
	v, ok := live.last(g.Name)
	if !ok {
		return out // нет свежего значения — только подпись: на телефоне будет «—»
	}
	switch g.How {
	case "dict":
		out.Text = numText(l, g.Name, v)
	case "minmax":
		if lo, hi, ok := live.minMax(g.Name, since); ok {
			lo, hi = summary.Round(lo), summary.Round(hi)
			out.Min, out.Max = &lo, &hi
		}
		fallthrough
	default:
		x := summary.Round(v)
		out.Value = &x
	}
	return out
}

// numText — «12 · Зажим закрыт» (или просто «12», если в справочнике нет)
func numText(l dictLookup, signal string, v float64) string {
	n := int64(math.Round(v))
	s := strconv.FormatInt(n, 10)
	if t, _ := l.text(signal, n); t != "" {
		s += " · " + t
	}
	return s
}

// tgActiveAlarms — активные аварии (без предупреждений и без зашунтированных: шунт ставят
// на долго висящую аварию, и станок не должен из-за неё весь день быть «в аварии»)
func (s *Server) tgActiveAlarms(ctx context.Context, plcID int64, now time.Time) ([]summary.Alarm, error) {
	evs, err := s.Store.ActiveEvents(ctx, plcID)
	if err != nil {
		return nil, err
	}
	shelves, err := s.Store.ActiveShelves(ctx, plcID, now)
	if err != nil {
		return nil, err
	}
	shelved := map[string]bool{}
	for _, sh := range shelves {
		shelved[sh.Signal] = true
	}
	var out []summary.Alarm
	for _, e := range evs {
		if e.Kind != store.KindAlarm || shelved[e.Signal] {
			continue
		}
		out = append(out, summary.Alarm{Text: e.Message, Since: e.Came.Local().Format("15:04")})
	}
	return out, nil
}

// ---------- Смена в процентах: файлы текущей смены ----------

// tgShift — доли текущей смены. Читаются все файлы смены (после правки сигналов или
// перезапуска их несколько), из каждого — только сигнал режима и биты состояний.
// Время без записи — «нет связи»: что тогда было со станком, мы не знаем.
func (s *Server) tgShift(ctx context.Context, pl store.PLC, mode string, rules summary.Rules, now time.Time) (*summary.Shift, error) {
	return s.tgShiftOf(ctx, pl, mode, rules, archive.ShiftOf(now), now)
}

// tgShiftOf — доли смены sh от её начала до момента to (для итога смены — до конца смены)
func (s *Server) tgShiftOf(ctx context.Context, pl store.PLC, mode string, rules summary.Rules, sh archive.Shift, now time.Time) (*summary.Shift, error) {
	name := "А"
	if sh.Name == "B" {
		name = "Б"
	}
	out := &summary.Shift{Name: name, From: sh.Start().Format("15:04")}
	tl := summary.Timeline{From: sh.Start().UnixMilli(), To: now.UnixMilli(), Bits: map[string][]steps.Step{}}

	names := []string{}
	if mode != "" {
		names = append(names, mode)
	}
	for _, r := range rules.States {
		if r.Bit != "" {
			names = append(names, r.Bit)
		}
	}
	for _, path := range shiftFiles(s.DataRoot, pl.Name, sh) {
		f, err := archive.LoadSignals(path, names)
		if err != nil {
			continue // файл, который не читается, — как время без записи
		}
		// Запись была — по временам первой группы (все группы опрашиваются одним соединением)
		if len(f.Groups) > 0 && len(f.Header.Groups) > 0 {
			times := f.Groups[0].Times
			j := steps.Journal(times, make([]float64, len(times)), 3*f.Header.Groups[0].PeriodMs)
			for _, st := range j {
				tl.Online = append(tl.Online, steps.Span{From: st.From, To: st.To})
			}
		}
		for _, ref := range pickSignals(f, names) {
			if !f.Has(ref.g, ref.i) {
				continue
			}
			j := steps.Journal(f.Groups[ref.g].Times, f.Groups[ref.g].Values[ref.i], 3*ref.PeriodMs)
			if ref.Name == mode {
				tl.Mode = append(tl.Mode, j...)
			} else {
				tl.Bits[ref.Name] = append(tl.Bits[ref.Name], j...)
			}
		}
	}

	if rules.Alarm {
		// Аварии смены: пришедшие за сутки до её начала тоже — могли ещё идти в смену
		evs, err := s.Store.Events(ctx, store.EventFilter{PLCID: pl.ID, From: sh.Start().Add(-24 * time.Hour),
			To: now.Add(time.Second), Kind: store.KindAlarm, Limit: 100000})
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			to := now
			if !e.Went.IsZero() {
				to = e.Went
			}
			tl.Alarms = append(tl.Alarms, steps.Span{From: e.Came.UnixMilli(), To: to.UnixMilli()})
		}
	}
	out.Shares = summary.Shares(rules, tl)
	return out, nil
}

// shiftFiles — файлы смены по порядку записи: <ПЛК>\год\месяц\день\A_*.pla
func shiftFiles(root, plcName string, sh archive.Shift) []string {
	dir := filepath.Dir(archive.Path(root, plcName, sh, sh.Start(), 0))
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range list {
		if !e.IsDir() && strings.HasPrefix(e.Name(), sh.Name+"_") && strings.HasSuffix(e.Name(), ".pla") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out) // в имени — время начала: A_20261117-080000_g1.pla
	return out
}

// ---------- Значения «сейчас» из буфера Live ----------

// liveData — буфер Live одного ПЛК на момент now
type liveData struct {
	groups []Group
	now    int64
}

func (s *Server) liveValues(plcID int64, now time.Time) liveData {
	return liveData{groups: s.config(plcID).Groups, now: now.UnixMilli()}
}

// find — группа и номер сигнала в ней
func (d liveData) find(name string) (Group, int, bool) {
	if name == "" {
		return Group{}, 0, false
	}
	for _, g := range d.groups {
		for i, n := range g.Names {
			if n == name && g.Ring != nil {
				return g, i, true
			}
		}
	}
	return Group{}, 0, false
}

// last — последнее значение, если оно свежее (не старше трёх периодов опроса, но не меньше 3 с)
// и это число. Старое значение хуже, чем никакого: станок мог давно перейти в другой режим.
func (d liveData) last(name string) (float64, bool) {
	g, i, ok := d.find(name)
	if !ok {
		return 0, false
	}
	fresh := max(3*g.Period.Milliseconds(), 3000)
	pts := g.Ring.Since(d.now - fresh)
	if len(pts) == 0 || i >= len(pts[len(pts)-1].V) {
		return 0, false
	}
	v := pts[len(pts)-1].V[i]
	return v, archive.Finite(v)
}

// minMax — мин и макс с момента since (NaN — пропуск)
func (d liveData) minMax(name string, since time.Time) (lo, hi float64, ok bool) {
	g, i, found := d.find(name)
	if !found {
		return 0, 0, false
	}
	for _, p := range g.Ring.Since(since.UnixMilli()) {
		if i >= len(p.V) || !archive.Finite(p.V[i]) {
			continue
		}
		if !ok || p.V[i] < lo {
			lo = p.V[i]
		}
		if !ok || p.V[i] > hi {
			hi = p.V[i]
		}
		ok = true
	}
	return lo, hi, ok
}

// ---------- Показ: «Что уйдёт наружу» ----------

// tgPreview — сводка для страницы: красиво отформатированный JSON и размер в байтах
type tgPreview struct {
	JSON  string
	Bytes int
	Err   string
}

func (s *Server) tgPreviewOf(ctx context.Context) tgPreview {
	sum, err := s.tgSummary(ctx, time.Now())
	if err != nil {
		return tgPreview{Err: err.Error()}
	}
	compact, _ := json.Marshal(sum)
	pretty, _ := json.MarshalIndent(sum, "", "  ")
	return tgPreview{JSON: string(pretty), Bytes: len(compact)}
}

// tgSummaryJSON — GET /telegram/summary.json: ровно то, что будет отправлено (только этот ПК)
func (s *Server) tgSummaryJSON(w http.ResponseWriter, r *http.Request) {
	if !s.tgOnlyHere(w, r) {
		return
	}
	sum, err := s.tgSummary(r.Context(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(sum)
}
