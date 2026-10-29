package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
)

// ---------- Анализ по паспортам: ссылки в «Истории» запускают Python (34d) ----------
//
// GET /explore?plc=1&kind=trend    — тренды (scripts/trend.py)
// GET /explore?plc=1&kind=anomaly  — необычные циклы последней смены (scripts/anomaly.py)
//
// Скрипт пишет отчёт в D:\plc-data\explore\<ПЛК>\<вид>\report.html, сервер отдаёт его в браузер.
// Отчёт свежее 10 минут не пересчитывается: открыли пять человек — Python запустился один раз.
// &fresh=1 — пересчитать сейчас.
//
// GET, а не POST: анализ ничего не меняет в настройках, поэтому смотреть его могут и дежурные
// с любого ПК. От нагрузки защищают «один анализ за раз» и эти 10 минут.

// exploreKinds — какие скрипты можно запускать. Имя файла берём ТОЛЬКО отсюда, а не из адреса:
// иначе через ?kind=… можно было бы запустить любой файл.
var exploreKinds = map[string]string{
	"trend":   "trend.py",
	"anomaly": "anomaly.py",
}

const (
	exploreFresh   = 10 * time.Minute // отчёт моложе — отдаём готовый
	exploreTimeout = 3 * time.Minute  // скрипт дольше — что-то не так, останавливаем
)

// explore — GET /explore
func (s *Server) explore(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	script, ok := exploreKinds[kind]
	if !ok {
		http.Error(w, "неизвестный вид анализа: "+kind, http.StatusBadRequest)
		return
	}
	base, pl, err := s.plcBase(r)
	if err != nil {
		http.Error(w, friendly(err).Error(), http.StatusNotFound)
		return
	}
	out := filepath.Join(s.DataRoot, "explore", archive.SafeName(pl.Name), kind)
	report := filepath.Join(out, "report.html")

	// Один анализ за раз: второй запрос ждёт первого — и потом получает уже готовый отчёт
	s.exploreMu.Lock()
	defer s.exploreMu.Unlock()

	if fi, err := os.Stat(report); err != nil || time.Since(fi.ModTime()) > exploreFresh || q.Has("fresh") {
		args := []string{filepath.Join(s.Scripts, script), base, out}
		if kind == "anomaly" {
			args = append(args, s.anomalyArgs(r.Context(), pl.ID)...)
		}
		if err := s.runPython(r.Context(), args); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store") // «обновить» в браузере — свежий отчёт, а не из кеша
	http.ServeFile(w, r, report)
}

// anomalyArgs — ссылки «график ↗» в отчёте: номер ПЛК и сигналы шага и режима из настройки паспорта.
// --url "" — ссылки от корня сайта (/view.html?…): работают с любого ПК, каким бы адресом ни открыли.
func (s *Server) anomalyArgs(ctx context.Context, plcID int64) []string {
	a := []string{"--plc", strconv.FormatInt(plcID, 10), "--url", ""}
	if p, err := s.Store.Passport(ctx, plcID); err == nil {
		a = append(a, "--step", p.Step)
		if p.Mode != "" {
			a = append(a, "--mode", p.Mode)
		}
	}
	return a
}

// runPython — запустить скрипт и дождаться. Ошибка — с последними строками того, что скрипт
// написал в stderr: там Python объясняет, что не так («в … нет паспортов», traceback).
func (s *Server) runPython(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, exploreTimeout)
	defer cancel()

	py := s.Python
	if py == "" {
		py = "python"
	}
	command := s.command
	if command == nil {
		command = exec.CommandContext
	}
	// Аргументы — списком, без командной строки: пробелы и кавычки в путях ничего не ломают
	cmd := command(ctx, py, args...)
	// Русский текст ошибок — в UTF-8, а не cp866. cmd.Environ(), а не os.Environ(): если окружение
	// у команды уже задано (в тестах так), не затираем его, а дополняем
	cmd.Env = append(cmd.Environ(), "PYTHONIOENCODING=utf-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("Python не найден (%q) — укажите путь к python.exe параметром -python", py)
	case ctx.Err() == context.DeadlineExceeded:
		return fmt.Errorf("анализ идёт дольше %v — остановлен", exploreTimeout)
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 3000 { // traceback бывает длинным — главное в конце
		msg = "…" + msg[len(msg)-3000:]
	}
	return fmt.Errorf("%s: %v\n\n%s", filepath.Base(args[0]), err, msg)
}
