package web

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// eventView — авария в виде, готовом для шаблона
type eventView struct {
	Came, Went, Dur string
	PLC             string
	Signal, Message string
	Kind            string // «авария» / «предупреждение»
	Warn            bool   // предупреждение (жёлтое), иначе авария (красная)
	Active          bool   // ещё не ушла
}

func toView(e store.Event, plcName string, now time.Time) eventView {
	v := eventView{
		Came:    fmtEventTime(e.Came, e.Came),
		PLC:     plcName,
		Signal:  e.Signal,
		Message: e.Message,
		Kind:    kindTitle(e.Kind),
		Warn:    e.Kind == store.KindWarning,
		Active:  e.Active(),
	}
	if v.Active {
		// Идёт сейчас: до секунды («12 с»), миллисекунды тут только мелькают
		v.Dur = fmtDurMs(now.Sub(e.Came).Truncate(time.Second).Milliseconds())
		if now.Sub(e.Came) < time.Minute {
			v.Dur = fmtDurSec(now.Sub(e.Came))
		}
	} else {
		v.Went = fmtEventTime(e.Went, e.Came)
		v.Dur = fmtDurMs(e.Went.Sub(e.Came).Milliseconds())
	}
	return v
}

// fmtDurSec — короткая длительность целыми секундами: «12 с»
func fmtDurSec(d time.Duration) string {
	return strconv.Itoa(int(d.Seconds())) + " с"
}

// fmtEventTime — время с миллисекундами (опрос до 10 мс, важен порядок аварий в лавине).
// Если день не тот, что у прихода аварии (например, ушла после полуночи), — с датой.
func fmtEventTime(t, ref time.Time) string {
	if t.YearDay() != ref.YearDay() || t.Year() != ref.Year() {
		return t.Format("02.01 15:04:05.000")
	}
	return t.Format("15:04:05.000")
}

// ---------- Панель активных аварий на Live ----------

func (s *Server) alarmsPanel(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ActiveEvents(r.Context(), s.reqPLCID(r))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	// Аварии выше предупреждений; внутри — по времени прихода (Stable сохраняет порядок из базы)
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Kind == store.KindAlarm && list[j].Kind != store.KindAlarm
	})

	now := time.Now()
	views := make([]eventView, len(list))
	alarms, warns := 0, 0
	for i, e := range list {
		views[i] = toView(e, "", now)
		if views[i].Warn {
			warns++
		} else {
			alarms++
		}
	}
	render(w, "alarms", map[string]any{
		"List": views, "Alarms": alarms, "Warns": warns, "PLCID": s.reqPLCID(r),
	})
}

// ---------- Журнал событий ----------

// eventsQuery — фильтр журнала из запроса: ?plc=0&day=2026-10-23&kind=alarm
type eventsQuery struct {
	PLCID int64  // 0 — все ПЛК
	Day   string // 2006-01-02
	Kind  string // "" / alarm / warning
}

func parseEventsQuery(r *http.Request) eventsQuery {
	q := r.URL.Query()
	eq := eventsQuery{Day: q.Get("day"), Kind: q.Get("kind")}
	eq.PLCID, _ = strconv.ParseInt(q.Get("plc"), 10, 64) // нет или ошибка — 0, все ПЛК
	if _, err := time.ParseInLocation("2006-01-02", eq.Day, time.Local); err != nil {
		eq.Day = time.Now().Format("2006-01-02") // по умолчанию — сегодня
	}
	if eq.Kind != store.KindAlarm && eq.Kind != store.KindWarning {
		eq.Kind = ""
	}
	return eq
}

func (s *Server) eventViews(r *http.Request, eq eventsQuery) ([]eventView, error) {
	plcs, err := s.Store.PLCs(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(plcs))
	for _, p := range plcs {
		names[p.ID] = p.Name
	}

	from, _ := time.ParseInLocation("2006-01-02", eq.Day, time.Local)
	list, err := s.Store.Events(r.Context(), store.EventFilter{
		PLCID: eq.PLCID, From: from, To: from.AddDate(0, 0, 1), Kind: eq.Kind,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := make([]eventView, len(list))
	for i, e := range list {
		out[i] = toView(e, names[e.PLCID], now)
	}
	return out, nil
}

func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request) {
	eq := parseEventsQuery(r)
	plcs, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	list, err := s.eventViews(r, eq)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "events", map[string]any{"Q": eq, "PLCs": plcs, "List": list})
}

// eventsList — только таблица (для HTMX: сменили фильтр или пришло время обновить)
func (s *Server) eventsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.eventViews(r, parseEventsQuery(r))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	render(w, "eventlist", list)
}
