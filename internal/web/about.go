package web

import (
	"encoding/json"
	"net/http"

	"github.com/KudinovArtemilus/plc-analyzer/internal/about"
)

// aboutJSON — GET /api/about: подпись программы для страниц без шаблона (Live, просмотр
// записи) и для картинки PNG — их рисует браузер, а имя, сборка и автор живут в пакете about.
func (s *Server) aboutJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"name":   about.Name,
		"author": about.Author,
		"build":  about.Build(),
		"line":   about.Line(),
	})
}
