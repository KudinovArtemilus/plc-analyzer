package web

import (
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

var recTmpl = template.Must(template.New("rec").Parse(`
<div class="bar {{if .Bad}}bad{{else}}ok{{end}}">
  <b>●</b> Запись:
  {{if .File}}{{.File}} — {{.Size}}{{else}}ожидание данных{{end}}
  {{if .Dropped}}| <b>потеряно точек: {{.Dropped}}</b>{{end}}
  {{if .Bad}}| ОШИБКА: {{.LastErr}}{{end}}
</div>`))

func (s *Server) recStatus(w http.ResponseWriter, r *http.Request) {
	if s.Units == nil {
		return
	}
	_, st, _ := s.Units(s.reqPLCID(r))
	v := struct {
		archive.Status
		Size string
		Bad  bool
	}{
		Status: st,
		Size:   fmtBytes(st.Bytes),
		Bad:    st.LastErr != "" && time.Since(st.ErrAt) < time.Minute,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	recTmpl.Execute(w, v)
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d КБ", n/1024)
	}
}
