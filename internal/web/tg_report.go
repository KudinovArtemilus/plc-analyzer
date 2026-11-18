package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgnotify"
)

// ---------- Итог смены в Telegram (шаг 45e) ----------
//
// После 08:00 и 20:00 бот присылает одно сообщение: по каждому станку с «показывать» —
// доли смены (как полоска на табло) и аварии за смену: сколько, сколько времени, какие чаще.
// Это та самая ежедневная записка руководству, только сама.

// ReportDelay — итог собирается через минуту после конца смены: последние порции записи
// (раз в 2 с) успевают лечь в файл, а аварии, ушедшие в 20:00:00, — в журнал
const ReportDelay = time.Minute

// shiftKey — «2026-11-18 A»: за какую смену итог уже отправлен
func shiftKey(sh archive.Shift) string { return sh.Date.Format("2006-01-02") + " " + sh.Name }

// TGReport — для бота: вызывается каждые 2 с; вернёт текст ровно один раз после конца смены
// (или сразу — после кнопки «прислать итог прошлой смены сейчас»)
func (s *Server) TGReport(ctx context.Context, now time.Time) (string, error) {
	on, last, err := s.Store.TGReport(ctx)
	if err != nil || !on {
		return "", err
	}
	cur := archive.ShiftOf(now)
	prev := archive.ShiftOf(cur.Start().Add(-time.Second))
	key := shiftKey(prev)
	switch {
	case last == "": // только что включили — первый итог в конце текущей смены, не задним числом
		return "", s.Store.SetTGReportLast(ctx, key)
	case last == key:
		return "", nil // уже отправлен
	case last != store.TGReportNow && now.Before(cur.Start().Add(ReportDelay)):
		return "", nil // смена только кончилась — ждём минуту
	}
	text, err := s.tgShiftReport(ctx, prev)
	if err != nil {
		return "", err
	}
	return text, s.Store.SetTGReportLast(ctx, key)
}

// tgShiftReport — текст итога смены:
//
//	📋 Итог: смена А 18.11 (08:00–20:00)
//
//	Мехобработка
//	Шлифовка: Автомат 71% · Ручной 18% · Авария 6% · Нет связи 5%
//	   аварий 4, всего 43 мин; больше всего: Дверь открыта ×3 (25 мин), Перегрев ×1 (18 мин)
func (s *Server) tgShiftReport(ctx context.Context, sh archive.Shift) (string, error) {
	title := "А"
	if sh.Name == "B" {
		title = "Б"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📋 Итог: смена %s %s (%s–%s)\n", title, sh.Date.Format("02.01"),
		sh.Start().Format("15:04"), sh.End().Format("15:04"))

	areas, err := s.Store.Areas(ctx)
	if err != nil {
		return "", err
	}
	plcs, err := s.Store.PLCs(ctx)
	if err != nil {
		return "", err
	}
	byArea := map[int64][]string{}
	shown := 0
	for _, pl := range plcs {
		m, err := s.Store.TGMachine(ctx, pl.ID)
		if err != nil {
			return "", err
		}
		if !m.Enabled {
			continue
		}
		line, err := s.tgMachineReport(ctx, pl, m, sh)
		if err != nil {
			return "", err
		}
		byArea[pl.AreaID] = append(byArea[pl.AreaID], line)
		shown++
	}
	write := func(name string, lines []string) {
		if len(lines) > 0 {
			b.WriteString("\n" + name + "\n" + strings.Join(lines, "\n") + "\n")
		}
	}
	for _, a := range areas {
		write(a.Name, byArea[a.ID])
		delete(byArea, a.ID)
	}
	var rest []string
	for _, l := range byArea {
		rest = append(rest, l...)
	}
	sort.Strings(rest)
	write("Без участка", rest)
	if shown == 0 {
		b.WriteString("\nНи один станок не отмечен «показывать» на странице «Telegram».")
	}

	out := strings.TrimRight(b.String(), "\n")
	if utf8.RuneCountInString(out) > summary.TextMax {
		out = string([]rune(out)[:summary.TextMax-20]) + "\n… (не поместилось)"
	}
	return out, nil
}

// tgMachineReport — строки одного станка
func (s *Server) tgMachineReport(ctx context.Context, pl store.PLC, m store.TGMachine, sh archive.Shift) (string, error) {
	name := pl.Name
	if m.Label != "" {
		name = m.Label
	}
	mode, rules, err := s.tgRules(ctx, pl.ID, m)
	if err != nil {
		return "", err
	}
	shift, err := s.tgShiftOf(ctx, pl, mode, rules, sh, sh.End())
	if err != nil {
		return "", err
	}
	var parts []string
	for _, x := range shift.Shares {
		parts = append(parts, fmt.Sprintf("%s %s%%", x.Name, pctText(x.Pct)))
	}
	line := name + ": " + strings.Join(parts, " · ")
	if len(parts) == 0 {
		line = name + ": состояния не настроены"
	}
	al, err := s.tgShiftAlarms(ctx, pl.ID, sh)
	if err != nil {
		return "", err
	}
	return line + "\n   " + al, nil
}

// tgShiftAlarms — «аварий 4, всего 43 мин; больше всего: Дверь открыта ×3 (25 мин), …»
// Считаются аварии, которые шли в смену (и перешедшие из прошлой); время — только внутри смены;
// перекрывающиеся — один раз.
func (s *Server) tgShiftAlarms(ctx context.Context, plcID int64, sh archive.Shift) (string, error) {
	from, to := sh.Start(), sh.End()
	evs, err := s.tgShiftEvents(ctx, plcID, from, to)
	if err != nil {
		return "", err
	}
	if len(evs) == 0 {
		return "аварий не было", nil
	}
	type group struct {
		text  string
		count int
		dur   time.Duration
	}
	groups := map[string]*group{}
	var spans [][2]time.Time
	for _, e := range evs {
		start, end := e.Came, e.Went
		if start.Before(from) {
			start = from
		}
		if end.IsZero() || end.After(to) {
			end = to
		}
		g := groups[e.Message]
		if g == nil {
			g = &group{text: e.Message}
			groups[e.Message] = g
		}
		g.count++
		g.dur += end.Sub(start)
		spans = append(spans, [2]time.Time{start, end})
	}
	// Общее время в аварии: перекрывающиеся аварии — один раз
	sort.Slice(spans, func(a, b int) bool { return spans[a][0].Before(spans[b][0]) })
	var total time.Duration
	var curFrom, curTo time.Time
	for i, sp := range spans {
		if i == 0 || sp[0].After(curTo) {
			total += curTo.Sub(curFrom)
			curFrom, curTo = sp[0], sp[1]
		} else if sp[1].After(curTo) {
			curTo = sp[1]
		}
	}
	total += curTo.Sub(curFrom)

	list := make([]*group, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].dur != list[b].dur {
			return list[a].dur > list[b].dur
		}
		return list[a].text < list[b].text
	})
	var top []string
	for i, g := range list {
		if i == 3 {
			break
		}
		top = append(top, fmt.Sprintf("%s ×%d (%s)", g.text, g.count, tgnotify.Duration(g.dur)))
	}
	return fmt.Sprintf("аварий %d, всего %s; больше всего: %s", len(evs), tgnotify.Duration(total), strings.Join(top, ", ")), nil
}

// pctText — 71 → «71», 6.25 → «6,3»
func pctText(p float64) string {
	if p == math.Trunc(p) {
		return fmt.Sprint(int(p))
	}
	return strings.Replace(fmt.Sprintf("%.1f", p), ".", ",", 1)
}

// ---------- Страница ----------

// tgReportSave — POST /telegram/report: галочка «присылать итог смены»
func (s *Server) tgReportSave(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.SetTGReport(r.Context(), who(r), checked(r, "on")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/telegram#report", http.StatusSeeOther)
}

// tgReportNow — POST /telegram/report/now: прислать итог прошлой смены сейчас (проверить)
func (s *Server) tgReportNow(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RequestTGReport(r.Context(), who(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/telegram#report", http.StatusSeeOther)
}
