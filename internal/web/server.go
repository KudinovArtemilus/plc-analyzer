package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/buffer"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

//go:embed static
var static embed.FS

type Group struct {
	Period time.Duration
	Names  []string
	Kinds  []string // тип каждого сигнала: trend / alarm / warning (по порядку Names)
	Ring   *buffer.Ring
}

// config — состав дорожек одного ПЛК
type config struct {
	Gen    int
	Groups []Group
}

// emptyConfig — ОДИН общий объект «нет конфигурации».
// Если создавать новый &config{} при каждом запросе, указатели всегда будут разными,
// и поток решит, что конфигурация меняется постоянно.
var emptyConfig = &config{}

type Server struct {
	Status   func() plc.Status // оставлено для совместимости; панели берут состояние через Units
	Store    *store.Store
	PLC      store.PLC             // ПЛК по умолчанию (первый)
	Rec      func() archive.Status // оставлено для совместимости
	DataRoot string
	Cache    *archive.Cache
	Units    func(id int64) (plc.Status, archive.Status, bool) // состояние любого ПЛК

	cfgs sync.Map // id ПЛК → *config
}

// SetConfig — новый состав дорожек ПЛК. Безопасно вызывать из любой горутины.
func (s *Server) SetConfig(plcID int64, gen int, groups []Group) {
	s.cfgs.Store(plcID, &config{Gen: gen, Groups: groups})
}

func (s *Server) config(plcID int64) *config {
	if v, ok := s.cfgs.Load(plcID); ok {
		return v.(*config)
	}
	return emptyConfig
}

// reqPLCID — ПЛК из запроса (?plc=2), по умолчанию первый
func (s *Server) reqPLCID(r *http.Request) int64 {
	if id, err := strconv.ParseInt(r.URL.Query().Get("plc"), 10, 64); err == nil {
		return id
	}
	return s.PLC.ID
}

func (s *Server) Handler() http.Handler {
	files, _ := fs.Sub(static, "static")

	mux := http.NewServeMux()
	mux.Handle("GET /", noCache(http.FileServerFS(files)))

	// Live
	mux.HandleFunc("GET /api/plcs", s.plcsJSON)
	mux.HandleFunc("GET /api/groups", s.groups)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /ui/status", s.status)
	mux.HandleFunc("GET /ui/recstatus", s.recStatus)
	mux.HandleFunc("GET /ui/alarms", s.alarmsPanel)

	// Сигналы
	mux.HandleFunc("GET /signals", s.signalsPage)
	mux.HandleFunc("POST /ui/signals", s.addSignal)
	mux.HandleFunc("GET /ui/signals/{id}", s.showRow)
	mux.HandleFunc("GET /ui/signals/{id}/edit", s.editRow)
	mux.HandleFunc("PUT /ui/signals/{id}", s.saveSignal)
	mux.HandleFunc("POST /ui/signals/{id}/toggle", s.toggleSignal)
	mux.HandleFunc("DELETE /ui/signals/{id}", s.deleteSignal)

	mux.HandleFunc("GET /ui/plc", s.showPLC)
	mux.HandleFunc("GET /ui/plc/edit", s.editPLC)
	mux.HandleFunc("PUT /ui/plc", s.savePLC)

	// История и выгрузка
	mux.HandleFunc("GET /history", s.historyPage)
	mux.HandleFunc("GET /ui/history/pick", s.historyPick)
	mux.HandleFunc("GET /api/history/meta", s.historyMeta)
	mux.HandleFunc("GET /api/history/data", s.historyData)
	mux.HandleFunc("GET /api/history/csv", s.historyCSV)

	// Слепки
	mux.HandleFunc("POST /api/snapshots", s.createSnapshot)
	mux.HandleFunc("GET /api/snapshots/categories", s.snapshotCategories)
	mux.HandleFunc("GET /snapshots", s.snapshotsPage)
	mux.HandleFunc("GET /ui/snapshots", s.snapshotsList)

	// Журнал событий
	mux.HandleFunc("GET /events", s.eventsPage)
	mux.HandleFunc("GET /ui/events", s.eventsList)

	// Шунты аварий
	mux.HandleFunc("GET /shelves", s.shelvesPage)
	mux.HandleFunc("GET /ui/shelve", s.shelveForm)
	mux.HandleFunc("POST /ui/shelves", s.addShelf)
	mux.HandleFunc("POST /ui/shelves/{id}/remove", s.removeShelf)

	// ПЛК
	mux.HandleFunc("GET /plcs", s.plcsPage)
	mux.HandleFunc("POST /ui/plcs", s.addPLC)
	mux.HandleFunc("GET /ui/plcs/{id}", s.plcRow)
	mux.HandleFunc("GET /ui/plcs/{id}/edit", s.plcEditRow)
	mux.HandleFunc("PUT /ui/plcs/{id}", s.updatePLC)
	mux.HandleFunc("GET /ui/plcs/{id}/state", s.plcState)

	return mux
}

// plcsJSON — список ПЛК для выпадающего списка на странице графиков
func (s *Server) plcsJSON(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type item struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		IP   string `json:"ip"`
	}
	out := []item{}
	for _, p := range list {
		out = append(out, item{ID: p.ID, Name: p.Name, IP: p.IP})
	}
	writeJSON(w, out)
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	type groupJSON struct {
		PeriodMs int64    `json:"period_ms"`
		Names    []string `json:"names"`
		Kinds    []string `json:"kinds"` // Live по умолчанию прячет аварии и предупреждения
	}
	c := s.config(s.reqPLCID(r))
	out := make([]groupJSON, len(c.Groups))
	for i, g := range c.Groups {
		out[i] = groupJSON{PeriodMs: g.Period.Milliseconds(), Names: g.Names, Kinds: g.Kinds}
	}
	writeJSON(w, out)
}

// ---- Панель статуса связи ----

var statusTmpl = template.Must(template.New("status").Parse(`
<div class="bar {{if .Connected}}ok{{else}}bad{{end}}">
  <b>●</b> {{.IP}} —
  {{if .Connected}}связь есть{{else}}НЕТ СВЯЗИ уже {{.Down}}{{end}}
  | {{.Rate}} чтений/с | макс {{.Max}} | ошибок: {{.Errors}} | простоев: {{.Outages}}
</div>`))

type statusView struct {
	plc.Status
	Down string
	Max  string
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	var st plc.Status
	if s.Units != nil {
		st, _, _ = s.Units(s.reqPLCID(r))
	}
	v := statusView{
		Status: st,
		Down:   time.Since(st.ChangedAt).Round(time.Second).String(),
		Max:    st.MaxDur.Round(time.Millisecond).String(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	statusTmpl.Execute(w, v)
}

// ---- Поток данных (SSE) ----

type streamMsg struct {
	G   int            `json:"g"`
	Pts []buffer.Point `json:"pts"`
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "потоковая передача не поддерживается", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	plcID := s.reqPLCID(r)
	c := s.config(plcID)
	last := make([]int64, len(c.Groups))

	if id := r.Header.Get("Last-Event-ID"); id != "" {
		genStr, times, _ := strings.Cut(id, ":")
		if g, err := strconv.Atoi(genStr); err == nil && g == c.Gen {
			parts := strings.Split(times, ",")
			if len(parts) == len(last) {
				for i, p := range parts {
					if v, err := strconv.ParseInt(p, 10, 64); err == nil {
						last[i] = v
					}
				}
			}
		}
	}

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			// Сравниваем УКАЗАТЕЛИ: после перезапуска блока (смена IP) номер Gen
			// начинается заново с 1 и мог бы совпасть со старым, а объект — всегда новый.
			if cur := s.config(plcID); cur != c {
				fmt.Fprintf(w, "event: config\ndata: %d\n\n", cur.Gen)
				flusher.Flush()
				return
			}

			sent := false
			for gi, g := range c.Groups {
				pts := g.Ring.Since(last[gi])
				if len(pts) == 0 {
					continue
				}
				last[gi] = pts[len(pts)-1].T

				data, err := json.Marshal(streamMsg{G: gi, Pts: pts})
				if err != nil {
					return
				}
				fmt.Fprintf(w, "id: %s\ndata: %s\n\n", eventID(c.Gen, last), data)
				sent = true
			}
			if sent {
				flusher.Flush()
			}
		}
	}
}

func eventID(gen int, last []int64) string {
	parts := make([]string, len(last))
	for i, v := range last {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strconv.Itoa(gen) + ":" + strings.Join(parts, ",")
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}
