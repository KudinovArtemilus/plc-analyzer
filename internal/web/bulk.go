package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// ---------- Массовое добавление сигналов ----------
//
// Два способа: диапазон битов по шаблону и вставка таблицы из Excel (или CSV).
// Сначала — предпросмотр с ошибкой у каждой строки, потом «Добавить» — всё одной
// транзакцией. Сервер ничего не запоминает между шагами: «Добавить» присылает ту же
// форму, и проверка выполняется заново (страницу могли держать открытой час).

const maxBulk = 1000 // за один раз — не больше

// bulkRow — строка предпросмотра: что получилось и что с ней не так
type bulkRow struct {
	Line int // номер строки во вставке (или порядковый номер в диапазоне)
	Row  store.SignalRow
	Err  string
}

// ---------- Диапазон битов ----------

// genRange раскладывает count битов подряд, начиная с адреса start:
// DB100.DBX0.0, 0.1 … 0.7, 1.0 … В шаблонах {n} — номер (с firstN), {addr} — адрес.
func genRange(start string, count, firstN int, nameTpl, msgTpl, kind string, periodMs int64) ([]bulkRow, error) {
	a, err := plc.ParseAddress(start)
	if err != nil {
		return nil, err
	}
	switch {
	case a.Kind != 'X':
		return nil, errors.New("диапазон — только биты: начальный адрес вида DB100.DBX0.0")
	case count < 1 || count > maxBulk:
		return nil, fmt.Errorf("количество — от 1 до %d", maxBulk)
	case !strings.Contains(nameTpl, "{n}") && !strings.Contains(nameTpl, "{addr}"):
		return nil, errors.New("в шаблоне имени нужен {n} или {addr}, иначе все имена будут одинаковыми")
	}

	out := make([]bulkRow, count)
	for i := range out {
		addr := a.String()
		fill := func(tpl string) string {
			return strings.NewReplacer("{n}", strconv.Itoa(firstN+i), "{addr}", addr).Replace(tpl)
		}
		out[i] = bulkRow{Line: i + 1, Row: store.SignalRow{
			Name: fill(nameTpl), Addr: addr, Type: "BOOL", PeriodMs: periodMs,
			Scale: 1, Kind: kind, Message: fill(msgTpl),
		}}
		// Следующий бит: после 7-го — бит 0 следующего байта
		if a.Bit++; a.Bit == 8 {
			a.Bit, a.Byte = 0, a.Byte+1
		}
	}
	return out, nil
}

// ---------- Вставка из Excel / CSV ----------

// Порядок столбцов во вставке. Обязательны первые два; остальные можно не заполнять.
var bulkColumns = []string{"имя", "адрес", "тип данных", "период, мс", "тип сигнала",
	"сообщение", "коэф.", "смещ.", "ед."}

// parseTable разбирает текст, скопированный из Excel (столбцы через Tab) или CSV
// (через «;» или «,»). Строка с заголовками («имя…») пропускается.
func parseTable(text string, defPeriod int64) []bulkRow {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	sep := detectSep(lines)
	var out []bulkRow
	firstLine := true
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		cols := strings.Split(l, sep)
		for j := range cols {
			cols[j] = strings.Trim(strings.TrimSpace(cols[j]), `"`)
		}
		// Заголовок — только в первой НЕПУСТОЙ строке
		if head := strings.ToLower(cols[0]); firstLine && (head == "имя" || head == "name") {
			firstLine = false
			continue
		}
		firstLine = false
		out = append(out, parseLine(i+1, cols, defPeriod))
	}
	return out
}

// detectSep — разделитель столбцов по первой непустой строке: Tab (копия из Excel), «;» или «,».
// Отдельная функция с return, а не break внутри switch: в Go break в switch выходит
// только из switch, и цикл пошёл бы дальше — разделитель взялся бы с ПОСЛЕДНЕЙ строки.
func detectSep(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		switch {
		case strings.Contains(l, "\t"):
			return "\t"
		case strings.Contains(l, ";"):
			return ";"
		default:
			return ","
		}
	}
	return ","
}

func parseLine(n int, cols []string, defPeriod int64) bulkRow {
	col := func(i int) string {
		if i < len(cols) {
			return cols[i]
		}
		return ""
	}
	br := bulkRow{Line: n, Row: store.SignalRow{
		Name: col(0), Addr: col(1), Type: strings.ToUpper(col(2)),
		Message: col(5), Unit: col(8), PeriodMs: defPeriod,
	}}

	if br.Row.Name == "" || br.Row.Addr == "" {
		br.Err = "нужны хотя бы имя и адрес"
		return br
	}
	if br.Row.Type == "" { // для бита тип очевиден, для слова — нет (INT или WORD?)
		if a, err := plc.ParseAddress(br.Row.Addr); err == nil && a.Kind == 'X' {
			br.Row.Type = "BOOL"
		} else {
			br.Err = "укажите тип данных (INT, REAL…)"
			return br
		}
	}
	if s := col(3); s != "" {
		p, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			br.Err = "период: целое число миллисекунд"
			return br
		}
		br.Row.PeriodMs = p
	}
	kind, ok := parseKind(col(4))
	if !ok {
		br.Err = "тип сигнала: тренд, авария или предупреждение"
		return br
	}
	br.Row.Kind = kind

	var err error
	if br.Row.Scale, err = parseNum(col(6), 1); err != nil {
		br.Err = "коэффициент: число"
		return br
	}
	if br.Row.Bias, err = parseNum(col(7), 0); err != nil {
		br.Err = "смещение: число"
	}
	return br
}

// parseKind — тип сигнала по-русски или по-английски; пусто — тренд
func parseKind(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "тренд", "trend":
		return store.KindTrend, true
	case "авария", "alarm":
		return store.KindAlarm, true
	case "предупреждение", "предупр", "предупр.", "warning":
		return store.KindWarning, true
	}
	return "", false
}

// ---------- Проверка пачки ----------

// checkBulk проверяет каждую строку теми же правилами, что и форма «Новый сигнал»,
// плюс повторы имён — внутри пачки и с уже существующими сигналами ПЛК.
// Возвращает число строк с ошибками.
func checkBulk(rows []bulkRow, plcID int64, existing []store.SignalRow) int {
	taken := make(map[string]bool, len(existing))
	for _, s := range existing {
		taken[strings.ToLower(s.Name)] = true
	}
	seen := map[string]int{}
	bad := 0
	for i := range rows {
		r := &rows[i]
		r.Row.PLCID, r.Row.Enabled = plcID, true
		if r.Err == "" {
			if err := checkSignal(&r.Row); err != nil {
				r.Err = err.Error()
			}
		}
		key := strings.ToLower(r.Row.Name)
		switch {
		case r.Err != "":
		case taken[key]:
			r.Err = "такое имя у этого ПЛК уже есть"
		case seen[key] > 0:
			r.Err = fmt.Sprintf("имя повторяется (строка %d)", seen[key])
		}
		if r.Row.Name != "" && seen[key] == 0 {
			seen[key] = r.Line
		}
		if r.Err != "" {
			bad++
		}
	}
	return bad
}

// ---------- Обработчики ----------

type bulkForm struct {
	PLCID   int64
	Columns []string
}

// bulkFormPage — GET /ui/signals/bulk?plc=1: форма (диапазон и вставка)
func (s *Server) bulkFormPage(w http.ResponseWriter, r *http.Request) {
	render(w, "bulkform", bulkForm{PLCID: s.reqPLCID(r), Columns: bulkColumns})
}

// bulkRows — строки из присланной формы (режим range или paste) и проверка
func (s *Server) bulkRows(r *http.Request) (plcID int64, rows []bulkRow, bad int, err error) {
	r.ParseForm()
	f := r.PostForm
	plcID, _ = strconv.ParseInt(f.Get("plc"), 10, 64)
	if _, err = s.Store.PLC(r.Context(), plcID); err != nil {
		return 0, nil, 0, errors.New("ПЛК не найден")
	}
	period, perr := strconv.ParseInt(strings.TrimSpace(f.Get("period_ms")), 10, 64)
	if perr != nil {
		return 0, nil, 0, errors.New("период: целое число миллисекунд")
	}

	switch f.Get("mode") {
	case "range":
		count, _ := strconv.Atoi(strings.TrimSpace(f.Get("count")))
		firstN, _ := strconv.Atoi(strings.TrimSpace(f.Get("first")))
		kind, ok := parseKind(f.Get("kind"))
		if !ok {
			return 0, nil, 0, errors.New("неизвестный тип сигнала")
		}
		rows, err = genRange(f.Get("start"), count, firstN,
			strings.TrimSpace(f.Get("name_tpl")), strings.TrimSpace(f.Get("msg_tpl")), kind, period)
		if err != nil {
			return 0, nil, 0, err
		}
	default: // paste
		rows = parseTable(f.Get("text"), period)
	}
	if len(rows) == 0 {
		return 0, nil, 0, errors.New("нет ни одной строки")
	}
	if len(rows) > maxBulk {
		return 0, nil, 0, fmt.Errorf("строк %d — за один раз не больше %d", len(rows), maxBulk)
	}

	existing, err := s.Store.Signals(r.Context(), plcID)
	if err != nil {
		return 0, nil, 0, friendly(err)
	}
	return plcID, rows, checkBulk(rows, plcID, existing), nil
}

type bulkPreview struct {
	FormID   string // какую форму прислать заново по «Добавить»
	Rows     []bulkRow
	OK, Bad  int
	Err      string
	Shown    int // сколько строк показано (длинный список обрезаем)
	Hidden   int
	FirstBad []bulkRow
}

func makePreview(formID string, rows []bulkRow, bad int, err error) bulkPreview {
	if formID != "bulkRange" && formID != "bulkPaste" {
		formID = "bulkPaste" // имя формы приходит из браузера — подставляем только известные
	}
	p := bulkPreview{FormID: formID}
	if err != nil {
		p.Err = err.Error()
		return p
	}
	p.OK, p.Bad = len(rows)-bad, bad
	// Длинный диапазон целиком не показываем: первые 50 строк и все строки с ошибками
	const show = 50
	for i, r := range rows {
		if i < show {
			p.Rows = append(p.Rows, r)
		} else if r.Err != "" {
			p.FirstBad = append(p.FirstBad, r)
		}
	}
	p.Shown = len(p.Rows)
	p.Hidden = len(rows) - p.Shown - len(p.FirstBad)
	return p
}

// bulkPreviewH — POST /ui/signals/bulk/preview: показать, что получится
func (s *Server) bulkPreviewH(w http.ResponseWriter, r *http.Request) {
	_, rows, bad, err := s.bulkRows(r)
	render(w, "bulkpreview", makePreview(r.PostForm.Get("form_id"), rows, bad, err))
}

// bulkAdd — POST /ui/signals/bulk/add: проверить заново и добавить всё одной транзакцией
func (s *Server) bulkAdd(w http.ResponseWriter, r *http.Request) {
	_, rows, bad, err := s.bulkRows(r)
	if err != nil || bad > 0 {
		render(w, "bulkpreview", makePreview(r.PostForm.Get("form_id"), rows, bad, err))
		return
	}
	list := make([]store.SignalRow, len(rows))
	for i, br := range rows {
		list[i] = br.Row
	}
	if err := s.Store.AddSignals(r.Context(), who(r), list); err != nil {
		render(w, "bulkpreview", makePreview(r.PostForm.Get("form_id"), rows, 0, friendly(err)))
		return
	}
	w.Header().Set("HX-Refresh", "true") // страница сигналов покажет новые строки
}
