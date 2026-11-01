package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Отметки на слепках: «вот здесь проблема» (36) ----------
//
// Слепок по аварии — это 5 минут до и 2 после по всем сигналам: сам момент аварии на таком
// участке — узкая полоска. Отметка — красная линия на всех дорожках, и слепок открывается
// сразу вокруг неё. Слепок по аварии получает отметки сам (момент каждой аварии), на любой
// слепок можно добавить отметку вручную: курсор A на нужное место → «⚑ Отметить».
//
// GET  /api/snapshots/marks?plc=&file=  — отметки слепка (JSON)
// POST /api/snapshots/marks?plc=&file=  — добавить {ms, text}; как всё изменяющее — только canEdit

// Участок, на котором открывается слепок: минута до отметки (что к ней привело) и 20 с после
const (
	markBefore = 60 * time.Second
	markAfter  = 20 * time.Second
)

// legacyMax — сколько отметок брать из комментария старого слепка (как maxMarks у новых)
const legacyMax = 10

// marksOf — отметки слепка. У слепков по аварии, сохранённых до отметок (до миграции 9),
// их нет в базе, но моменты аварий записаны в комментарии: «09:00:00.150  AL1 — текст».
// Берём, как и у новых слепков, только первый приход каждого сигнала и не больше legacyMax.
func marksOf(sn store.Snapshot) []store.Mark {
	if len(sn.Marks) > 0 || sn.Category != "авария" {
		return sn.Marks
	}
	from := time.UnixMilli(sn.FromMs)
	seen := map[string]bool{}
	var out []store.Mark
	for _, line := range strings.Split(sn.Comment, "\n") {
		clock, text, ok := strings.Cut(line, "  ")
		if !ok || len(out) == legacyMax {
			continue
		}
		signal, _, _ := strings.Cut(text, " — ")
		if seen[signal] {
			continue
		}
		seen[signal] = true
		t, err := time.ParseInLocation("15:04:05.000", clock, time.Local)
		if err != nil {
			continue
		}
		at := time.Date(from.Year(), from.Month(), from.Day(), t.Hour(), t.Minute(), t.Second(),
			t.Nanosecond(), time.Local)
		if at.Before(from) { // слепок через полночь: время меньше начала — это уже следующий день
			at = at.AddDate(0, 0, 1)
		}
		out = append(out, store.Mark{Ms: at.UnixMilli(), Text: text})
	}
	return out
}

func (s *Server) snapshotMarks(w http.ResponseWriter, r *http.Request) {
	sn, err := s.Store.SnapshotByFile(r.Context(), s.reqPLCID(r), r.URL.Query().Get("file"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, []store.Mark{}) // это не слепок, а запись смены — отметок нет, и это не ошибка
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	marks := marksOf(sn)
	if marks == nil {
		marks = []store.Mark{} // в JSON — [], а не null
	}
	writeJSON(w, marks)
}

func (s *Server) addSnapshotMark(w http.ResponseWriter, r *http.Request) {
	var m store.Mark
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&m); err != nil || m.Ms <= 0 {
		http.Error(w, "неверная отметка", http.StatusBadRequest)
		return
	}
	m.Text = strings.TrimSpace(m.Text)
	if m.Text == "" {
		m.Text = "отметка"
	}
	plc, file := s.reqPLCID(r), r.URL.Query().Get("file")
	sn, err := s.Store.SnapshotByFile(r.Context(), plc, file)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "отмечать можно только на слепке", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if m.Ms < sn.FromMs || m.Ms > sn.ToMs {
		http.Error(w, "отметка вне участка слепка", http.StatusBadRequest)
		return
	}
	// Слепок по аварии из старой версии: сначала переносим в базу отметки из комментария,
	// иначе после первой ручной отметки они перестали бы показываться
	if len(sn.Marks) == 0 {
		for _, old := range marksOf(sn) {
			if err := s.Store.AddMark(r.Context(), "система", plc, file, old); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	if err := s.Store.AddMark(r.Context(), who(r), plc, file, m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
