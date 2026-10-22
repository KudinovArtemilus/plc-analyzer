package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Создание слепка ----------

type snapshotReq struct {
	File     string   `json:"file"`
	Signals  []string `json:"signals"`
	FromMs   int64    `json:"from_ms"`
	ToMs     int64    `json:"to_ms"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Comment  string   `json:"comment"`
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req snapshotReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "неверный запрос", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Category = strings.TrimSpace(req.Category)
	req.Comment = strings.TrimSpace(req.Comment)
	switch {
	case req.Name == "":
		http.Error(w, "укажите название слепка", http.StatusBadRequest)
		return
	case len(req.Signals) == 0:
		http.Error(w, "не выбраны сигналы", http.StatusBadRequest)
		return
	case req.ToMs <= req.FromMs:
		http.Error(w, "пустой промежуток", http.StatusBadRequest)
		return
	}

	base, pl, err := s.plcBase(r)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	src, err := s.openFile(r, req.File)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h, chunks := archive.Extract(src, req.Signals, req.FromMs, req.ToMs)
	if len(chunks) == 0 {
		http.Error(w, "в выбранном промежутке нет точек", http.StatusBadRequest)
		return
	}
	h.Created = time.Now()

	// snapshots/S_20261021-193512.345.pla — миллисекунды, чтобы имена не совпали
	rel := path.Join("snapshots", "S_"+time.Now().Format("20060102-150405.000")+".pla")
	full := filepath.Join(base, filepath.FromSlash(rel))

	if err := writeSnapshot(full, h, chunks); err != nil {
		os.Remove(full) // недописанный файл не оставляем
		http.Error(w, "запись слепка: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var names []string
	for _, g := range h.Groups {
		for _, sg := range g.Signals {
			names = append(names, sg.Name)
		}
	}
	id, err := s.Store.AddSnapshot(r.Context(), who(r), store.Snapshot{
		PLCID: pl.ID, Name: req.Name, Category: req.Category, Comment: req.Comment,
		File: rel, FromMs: req.FromMs, ToMs: req.ToMs, Signals: names,
	})
	if err != nil {
		os.Remove(full) // без записи в базе файл никто не найдёт
		http.Error(w, friendly(err).Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

func writeSnapshot(path string, h archive.Header, chunks []archive.Chunk) error {
	wr, err := archive.Create(path, h)
	if err != nil {
		return err
	}
	for _, c := range chunks {
		if err := wr.WriteChunk(c); err != nil {
			wr.Close()
			return err
		}
	}
	return wr.Close()
}

func (s *Server) snapshotCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.Store.Categories(r.Context(), s.reqPLCID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, cats)
}

// ---------- Библиотека слепков ----------

type snapView struct {
	Name, Category, Comment string
	Range, Dur, Signals     string
	Created, Who, Link      string
}

func (s *Server) snapshotViews(r *http.Request, plcID int64, category string) ([]snapView, error) {
	list, err := s.Store.Snapshots(r.Context(), plcID, category)
	if err != nil {
		return nil, err
	}
	out := make([]snapView, 0, len(list))
	for _, sn := range list {
		q := url.Values{}
		q.Set("plc", fmt.Sprint(plcID))
		q.Set("file", sn.File)
		for _, n := range sn.Signals {
			q.Add("s", n)
		}
		out = append(out, snapView{
			Name: sn.Name, Category: sn.Category, Comment: sn.Comment,
			Range:   fmtMs(sn.FromMs) + " — " + timeFromMs(sn.ToMs).Format("15:04:05"),
			Dur:     fmtDurMs(sn.ToMs - sn.FromMs),
			Signals: strings.Join(sn.Signals, ", "),
			Created: sn.Created.Local().Format("02.01.2006 15:04"),
			Who:     sn.Who,
			Link:    "/view.html?" + q.Encode(),
		})
	}
	return out, nil
}

func (s *Server) snapshotsPage(w http.ResponseWriter, r *http.Request) {
	pl, err := s.Store.PLC(r.Context(), s.reqPLCID(r))
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	all, err := s.Store.PLCs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cats, _ := s.Store.Categories(r.Context(), pl.ID)
	list, err := s.snapshotViews(r, pl.ID, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "snapshots", map[string]any{"PLC": pl, "PLCs": all, "Cats": cats, "List": list})
}

func (s *Server) snapshotsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.snapshotViews(r, s.reqPLCID(r), r.URL.Query().Get("cat"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "snaplist", list)
}

// fmtDurMs: 3250 → "3,250 с", 130000 → "2 мин 10 с"
func fmtDurMs(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return strings.Replace(fmt.Sprintf("%.3f с", d.Seconds()), ".", ",", 1)
	}
	h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d ч %d мин", h, m)
	}
	return fmt.Sprintf("%d мин %d с", m, sec)
}
