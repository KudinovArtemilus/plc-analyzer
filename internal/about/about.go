// Package about — чья это программа и какая это сборка.
//
// Подпись берётся только отсюда: подвал страниц, картинка PNG и CSV из «Истории», паспорта
// циклов, заголовок файлов записи .pla, лог при запуске. Поменять имя или год — в одном месте.
package about

import (
	"fmt"
	"runtime/debug"
	"time"
)

const (
	Name   = "PLC Analyzer"
	Author = "Артём Кудинов"
	Year   = 2026 // год первой версии — для «© 2026»
	Repo   = "github.com/KudinovArtemilus/plc-analyzer"
)

// Build — какая это сборка: коммит git и его дата, например «9404bc7 от 02.11.2026».
//
// Ничего настраивать не нужно: go build (Go 1.18+) сам записывает в exe, из какого коммита
// он собран, если собирать в папке проекта с git. «+правки» — в папке были изменения,
// ещё не попавшие в коммит. «без git» — собрано не из папки с git (или это go test).
func Build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "без git"
	}
	var rev, at string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return buildText(rev, at, dirty)
}

// buildText — отдельно от Build, чтобы проверить в тесте: в go test сведений git нет
func buildText(rev, at string, dirty bool) string {
	if rev == "" {
		return "без git"
	}
	s := rev[:min(len(rev), 7)] // короткий хеш — как его показывает git log --oneline
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		s += " от " + t.Local().Format("02.01.2006")
	}
	if dirty {
		s += " +правки"
	}
	return s
}

// Line — подпись одной строкой: «PLC Analyzer · сборка 9404bc7 от 02.11.2026 · © 2026 Артём Кудинов»
func Line() string {
	return fmt.Sprintf("%s · сборка %s · © %d %s", Name, Build(), Year, Author)
}