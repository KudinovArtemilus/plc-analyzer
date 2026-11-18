package web

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/ask"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plot"
	"github.com/KudinovArtemilus/plc-analyzer/internal/stats"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgbot"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgnotify"
)

// ---------- Вопросы к боту обычными словами (шаг 45f) ----------
//
// «давление шлифовка 10 мин» → картинка графика из записей анализатора и статистика тем же кодом,
// что «Σ Статистика» в «Истории». Спросить можно только о станках с «показывать» и только
// о сигналах с их карточки на странице «Telegram» — наружу уходит лишь то, что там отмечено.

// CaptionMax — подпись к фото в Telegram — до 1024 символов
const CaptionMax = 1000

// tgCatalog — о чём можно спрашивать
func (s *Server) tgCatalog(ctx context.Context) ([]ask.Machine, error) {
	plcs, err := s.Store.PLCs(ctx)
	if err != nil {
		return nil, err
	}
	var out []ask.Machine
	for _, pl := range plcs {
		m, err := s.Store.TGMachine(ctx, pl.ID)
		if err != nil {
			return nil, err
		}
		if !m.Enabled {
			continue
		}
		am := ask.Machine{PLCID: pl.ID, Title: pl.Name, Names: []string{pl.Name}}
		if m.Label != "" {
			am.Title, am.Names = m.Label, append(am.Names, m.Label)
		}
		sigs, err := s.Store.TGSignals(ctx, pl.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range sigs {
			am.Signals = append(am.Signals, ask.Signal{ID: g.SignalID, Name: g.Name, Label: g.Label, Type: g.Type})
		}
		out = append(out, am)
	}
	return out, nil
}

// TGAsk — ответ бота на вопрос: картинка с подписью или текст
func (s *Server) TGAsk(ctx context.Context, text string, now time.Time) (tgbot.Reply, error) {
	cat, err := s.tgCatalog(ctx)
	if err != nil {
		return tgbot.Reply{}, err
	}
	low := strings.ToLower(strings.TrimSpace(text))
	if low == "помощь" || low == "/help" || low == "?" {
		return tgbot.Reply{Text: ask.Help(cat)}, nil
	}
	r := ask.Parse(text, cat)
	if r.Problem != "" {
		msg := r.Problem
		if len(r.Options) > 0 {
			msg += "\nМожно: " + strings.Join(r.Options, ", ")
		}
		return tgbot.Reply{Text: msg + "\n\nНапример: «давление шлифовка 10 мин». Все станки и сигналы — «помощь»."}, nil
	}

	from := now.Add(-time.Duration(r.Minutes) * time.Minute)
	if r.Shift {
		from = archive.ShiftOf(now).Start()
	}
	pl, err := s.Store.PLC(ctx, r.Machine.PLCID)
	if err != nil {
		return tgbot.Reply{}, err
	}
	names := make([]string, len(r.Signals))
	for i, sg := range r.Signals {
		names[i] = sg.Name
	}
	data := s.tgWindow(pl.Name, names, from, now)

	// Подпись: станок, участок времени, по сигналу — строка статистики
	var b strings.Builder
	period := fmt.Sprintf("%s–%s (%s)", from.Format("15:04"), now.Format("15:04"), tgnotify.Duration(now.Sub(from)))
	if r.Shift {
		period = "смена с " + from.Format("15:04") + " (" + tgnotify.Duration(now.Sub(from)) + ")"
	}
	fmt.Fprintf(&b, "📈 %s · %s\n", r.Machine.Title, period)
	dots := []string{"🔵", "🟠", "🟢", "🟣"}
	var series []plot.Series
	has := false
	for i, sg := range r.Signals {
		d := data[sg.Name]
		isBool := strings.EqualFold(sg.Type, "BOOL")
		series = append(series, plot.Series{T: d.t, V: d.v, Bool: isBool})
		line := sg.Label + ": "
		switch {
		case len(d.t) == 0:
			line += "записей нет"
		case !r.Stats:
			line = sg.Label
		case isBool:
			line += bitText(stats.Bits(d.t, d.v, from.UnixMilli(), now.UnixMilli(), 3*d.periodMs))
		default:
			line += analogText(stats.Analyze(d.t, d.v, from.UnixMilli(), now.UnixMilli()))
		}
		if len(d.t) > 0 {
			has = true
		}
		prefix := ""
		if r.Chart {
			prefix = fmt.Sprintf("%d %s ", i+1, dots[i%len(dots)]) // номер и цвет — как дорожка на картинке
		}
		b.WriteString(prefix + line + "\n")
	}
	caption := strings.TrimRight(b.String(), "\n")
	if !has {
		return tgbot.Reply{Text: caption + "\n\nЗа это время записей нет: анализатор не писал или не было связи с ПЛК."}, nil
	}
	if !r.Chart {
		return tgbot.Reply{Text: caption}, nil
	}
	png, err := plot.PNG(series, from.UnixMilli(), now.UnixMilli(), time.Local)
	if err != nil {
		return tgbot.Reply{}, err
	}
	if utf8.RuneCountInString(caption) > CaptionMax {
		caption = string([]rune(caption)[:CaptionMax-1]) + "…"
	}
	return tgbot.Reply{Text: caption, Photo: png}, nil
}

// tgSeries — точки одного сигнала за участок
type tgSeries struct {
	t        []int64
	v        []float64
	periodMs int64
}

// tgWindow — точки сигналов за [from, to] из файлов записи: текущей смены и, если участок
// начинается раньше, предыдущей. Из файлов читаются только нужные сигналы (LoadSignals).
func (s *Server) tgWindow(plcName string, names []string, from, to time.Time) map[string]*tgSeries {
	out := map[string]*tgSeries{}
	for _, n := range names {
		out[n] = &tgSeries{}
	}
	var shifts []archive.Shift
	for sh := archive.ShiftOf(to); sh.End().After(from) && len(shifts) < 3; sh = archive.ShiftOf(sh.Start().Add(-time.Second)) {
		shifts = append([]archive.Shift{sh}, shifts...) // по порядку времени
	}
	f0, f1 := from.UnixMilli(), to.UnixMilli()
	for _, sh := range shifts {
		for _, path := range shiftFiles(s.DataRoot, plcName, sh) {
			f, err := archive.LoadSignals(path, names)
			if err != nil {
				continue
			}
			for _, ref := range pickSignals(f, names) {
				if !f.Has(ref.g, ref.i) {
					continue
				}
				times, vals := f.Groups[ref.g].Times, f.Groups[ref.g].Values[ref.i]
				lo := sort.Search(len(times), func(k int) bool { return times[k] >= f0 })
				hi := sort.Search(len(times), func(k int) bool { return times[k] > f1 })
				d := out[ref.Name]
				d.t = append(d.t, times[lo:hi]...)
				d.v = append(d.v, vals[lo:hi]...)
				d.periodMs = ref.PeriodMs
			}
		}
	}
	return out
}

// analogText — «мин 170,1 (11:44:05) · макс 190,2 (11:47:31) · среднее 181,6»
func analogText(a stats.Analog) string {
	if a.N == 0 {
		return "записей нет"
	}
	t := func(ms int64) string { return time.UnixMilli(ms).Format("15:04:05") }
	s := fmt.Sprintf("мин %s (%s) · макс %s (%s) · среднее %s", num(a.Min), t(a.MinAt), num(a.Max), t(a.MaxAt), num(a.Mean))
	if a.Bad > 0 {
		s += fmt.Sprintf(" · не число: %d раз", a.Bad)
	}
	return s
}

// bitText — «включений 7 · в «1» 5 мин (50 %) · дольше всего 40 с (с 11:46:02)»
func bitText(b stats.Bit) string {
	if b.N == 0 {
		return "записей нет"
	}
	pct := 0.0
	if b.CoveredMs > 0 {
		pct = float64(b.OnMs) * 100 / float64(b.CoveredMs)
	}
	s := fmt.Sprintf("включений %d · в «1» %s (%s %%)", b.Rises, dur(b.OnMs), num(math.Round(pct*10)/10))
	if b.LongestMs > 0 {
		cut := ""
		if b.LongestCut {
			cut = "≥ "
		}
		s += fmt.Sprintf(" · дольше всего %s%s (с %s)", cut, dur(b.LongestMs), time.UnixMilli(b.LongestAt).Format("15:04:05"))
	}
	return s
}

// dur — «40 с», «5 мин 10 с», «2 ч 5 мин»
func dur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Round(time.Second)/time.Second))
	case d < time.Hour:
		m, sec := int(d/time.Minute), int(d%time.Minute/time.Second)
		if sec == 0 {
			return fmt.Sprintf("%d мин", m)
		}
		return fmt.Sprintf("%d мин %d с", m, sec)
	}
	return tgnotify.Duration(d)
}

// num — число для людей: до 2 знаков, запятая
func num(x float64) string {
	s := fmt.Sprintf("%.2f", x)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return strings.Replace(s, ".", ",", 1)
}
