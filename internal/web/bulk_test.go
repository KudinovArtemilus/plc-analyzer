package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestGenRange(t *testing.T) {
	rows, err := genRange("db100.dbx0.6", 4, 7, "AL_{n}", "Авария {n} ({addr})", store.KindAlarm, 100)
	if err != nil {
		t.Fatal(err)
	}
	// После бита 7 — бит 0 следующего байта
	want := []string{"DB100.DBX0.6", "DB100.DBX0.7", "DB100.DBX1.0", "DB100.DBX1.1"}
	for i, r := range rows {
		if r.Row.Addr != want[i] {
			t.Fatalf("строка %d: адрес %s, ожидался %s", i, r.Row.Addr, want[i])
		}
	}
	if rows[0].Row.Name != "AL_7" || rows[3].Row.Name != "AL_10" {
		t.Fatalf("имена %q … %q, ожидались AL_7 … AL_10", rows[0].Row.Name, rows[3].Row.Name)
	}
	if rows[2].Row.Message != "Авария 9 (DB100.DBX1.0)" || rows[2].Row.Type != "BOOL" {
		t.Fatalf("строка 3: %+v", rows[2].Row)
	}

	bad := []struct {
		start, tpl string
		count      int
	}{
		{"DB100.DBW0", "AL_{n}", 4},   // не бит
		{"DB100.DBX0.0", "AL", 4},     // все имена одинаковые
		{"DB100.DBX0.0", "AL_{n}", 0}, // нечего добавлять
		{"DB100.DBX0.0", "AL_{n}", maxBulk + 1},
	}
	for _, b := range bad {
		if _, err := genRange(b.start, b.count, 1, b.tpl, "", store.KindTrend, 100); err == nil {
			t.Errorf("genRange(%q, %d, %q) должен давать ошибку", b.start, b.count, b.tpl)
		}
	}
}

func TestParseTable(t *testing.T) {
	// Как копирует Excel: столбцы через Tab, строки через \r\n; первая строка — заголовки
	excel := "имя\tадрес\tтип данных\tпериод, мс\tтип сигнала\tсообщение\tкоэф.\tсмещ.\tед.\r\n" +
		"Давление\tDB11.DBW90\tint\t50\t\t\t0,1\t-40\tбар\r\n" +
		"\r\n" +
		"AL_Door\tDB100.DBX0.0\t\t\tавария\tОткрыта дверь\r\n" +
		"Плохо\tDB11.DBW92\r\n" // нет типа для слова — ошибка
	rows := parseTable(excel, 100)
	if len(rows) != 3 {
		t.Fatalf("строк %d, ожидалось 3 (заголовок и пустая пропущены)", len(rows))
	}
	p := rows[0].Row
	if rows[0].Err != "" || p.Type != "INT" || p.PeriodMs != 50 || p.Scale != 0.1 || p.Bias != -40 || p.Unit != "бар" {
		t.Fatalf("Давление: %+v, ошибка %q", p, rows[0].Err)
	}
	a := rows[1].Row
	if rows[1].Err != "" || a.Type != "BOOL" || a.Kind != store.KindAlarm || a.PeriodMs != 100 || a.Message != "Открыта дверь" {
		t.Fatalf("AL_Door (тип BOOL по адресу DBX, период по умолчанию): %+v, ошибка %q", a, rows[1].Err)
	}
	if rows[2].Err == "" || rows[2].Line != 5 {
		t.Fatalf("Плохо: должна быть ошибка в строке 5, получено %+v", rows[2])
	}

	// CSV из Excel «CSV (разделитель — точка с запятой)», без заголовка
	csv := "T1;DB1.DBD0;REAL;1000\nT2;DB1.DBD4;REAL;1000"
	if r := parseTable(csv, 100); len(r) != 2 || r[1].Row.Addr != "DB1.DBD4" || r[1].Row.PeriodMs != 1000 {
		t.Fatalf("CSV через «;»: %+v", r)
	}
}

func TestDetectSep(t *testing.T) {
	// Разделитель — по ПЕРВОЙ непустой строке, даже если в последней есть запятая
	if got := detectSep([]string{"", "a;b;c", "x,y"}); got != ";" {
		t.Fatalf("разделитель %q, ожидался «;»", got)
	}
}

func TestCheckBulk(t *testing.T) {
	rows := parseTable(strings.Join([]string{
		"AL_1\tDB100.DBX0.0\t\t\tавария\tТекст", // ок
		"AL_2\tDB100.DBX0.1\t\t\tавария",        // авария без текста
		"AL_1\tDB100.DBX0.2\t\t\tавария\tТекст", // повтор имени в пачке
		"OLD\tDB100.DBX0.3",                     // уже есть у ПЛК
		"T1\tDB1.DBW0\tREAL",                    // REAL по адресу слова
	}, "\n"), 100)
	existing := []store.SignalRow{{Name: "old"}}
	bad := checkBulk(rows, 7, existing)
	if bad != 4 {
		t.Fatalf("строк с ошибками %d, ожидалось 4: %+v", bad, rows)
	}
	if rows[0].Err != "" || rows[0].Row.PLCID != 7 || !rows[0].Row.Enabled {
		t.Fatalf("первая строка должна пройти: %+v", rows[0])
	}
	for i, want := range []string{"", "текст сообщения", "строка 1", "уже есть", ""} {
		if want != "" && !strings.Contains(rows[i].Err, want) {
			t.Errorf("строка %d: ошибка %q, ожидалось про «%s»", i+1, rows[i].Err, want)
		}
	}
	if rows[4].Err == "" {
		t.Error("REAL по адресу DBW должен давать ошибку")
	}
}

func TestBulkTemplates(t *testing.T) {
	ok := []bulkRow{{Line: 1, Row: store.SignalRow{Name: "AL_1", Addr: "DB100.DBX0.0", Type: "BOOL", PeriodMs: 100, Kind: store.KindAlarm, Message: "Авария <1>"}}}
	bad := []bulkRow{ok[0], {Line: 2, Row: store.SignalRow{Name: "AL_1"}, Err: "имя повторяется (строка 1)"}}
	cases := []struct {
		name string
		data any
		want []string
		not  []string
	}{
		{"bulkform", bulkForm{PLCID: 3, Columns: bulkColumns}, []string{
			`id="bulkRange"`, `id="bulkPaste"`, `name="plc" value="3"`, "AL_{n}", "тип сигнала", "windows-1251"}, nil},
		// Без ошибок — кнопка «Добавить» шлёт ту же форму
		{"bulkpreview", makePreview("bulkRange", ok, 0, nil), []string{
			`hx-include="#bulkRange"`, "Добавить 1", "Авария &lt;1&gt;", "авария"}, nil},
		// С ошибками — кнопки нет, строка с ошибкой выделена
		{"bulkpreview", makePreview("bulkPaste", bad, 1, nil), []string{
			"Строк с ошибками: 1", "bulk-bad", "имя повторяется"}, []string{"/ui/signals/bulk/add"}},
		// Чужое имя формы из браузера не подставляется
		{"bulkpreview", makePreview(`x" onclick="alert(1)`, ok, 0, nil), []string{`hx-include="#bulkPaste"`}, []string{"alert"}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		render(w, c.name, c.data)
		// Пробелы и переводы строк схлопываем, как это делает браузер: тест не должен
		// ломаться оттого, что редактор переформатировал шаблон и перенёс текст кнопки
		body := strings.Join(strings.Fields(w.Body.String()), " ")
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("шаблон %s: нет %q", c.name, s)
			}
		}
		for _, s := range c.not {
			if strings.Contains(body, s) {
				t.Errorf("шаблон %s: не должно быть %q", c.name, s)
			}
		}
	}
}

func TestMakePreviewLong(t *testing.T) {
	// 200 строк, ошибка в 150-й: показываем первые 50, «ещё 149», и строку с ошибкой отдельно
	rows := make([]bulkRow, 200)
	for i := range rows {
		rows[i].Line = i + 1
	}
	rows[149].Err = "ошибка"
	p := makePreview("bulkRange", rows, 1, nil)
	if len(p.Rows) != 50 || p.Hidden != 149 || len(p.FirstBad) != 1 || p.FirstBad[0].Line != 150 {
		t.Fatalf("показано %d, скрыто %d, ошибок отдельно %d", len(p.Rows), p.Hidden, len(p.FirstBad))
	}
}
