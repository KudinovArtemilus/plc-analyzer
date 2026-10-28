package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Весь путь через настоящий сервер и настоящую базу:
// создать набор → отметить сигналы → набор виден в /api/sets
func TestSetsPages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plcID, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Пресс", IP: "192.168.0.1", Slot: 2})
	var ids []int64
	for _, n := range []string{"Pressure", "OilTemp", "Step"} {
		id, _ := st.AddSignal(ctx, "тест", store.SignalRow{PLCID: plcID, Name: n, Addr: "DB1.DBW0",
			Type: "INT", PeriodMs: 100, Scale: 1, Enabled: true})
		ids = append(ids, id)
	}
	pl, _ := st.PLC(ctx, plcID)
	h := (&Server{Store: st, PLC: pl}).Handler()

	do := func(method, target string, form url.Values) *httptest.ResponseRecorder {
		var r *http.Request
		if form != nil {
			r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		r.RemoteAddr = "127.0.0.1:50000" // этот ПК — менять можно
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	plc := "?plc=" + itoa(plcID)

	// Создать: переход на страницу набора
	w := do("POST", "/sets"+plc, url.Values{"name": {"Гидравлика"}})
	loc := w.Header().Get("Location")
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/sets/") {
		t.Fatalf("создание: код %d, Location %q, тело %s", w.Code, loc, w.Body.String())
	}
	setPath := strings.SplitN(loc, "?", 2)[0]

	// Страница набора: галочки всех сигналов ПЛК
	if b := do("GET", loc, nil).Body.String(); !strings.Contains(b, `name="sig" value="`+itoa(ids[2])+`"`) {
		t.Fatalf("страница набора без галочек:\n%s", b)
	}

	// Отметить два сигнала
	w = do("POST", setPath+plc, url.Values{"name": {"Гидравлика"}, "sig": {itoa(ids[0]), itoa(ids[1])}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("сохранение: код %d\n%s", w.Code, w.Body.String())
	}
	// Отмеченные — с checked
	if b := do("GET", loc, nil).Body.String(); !strings.Contains(b, `value="`+itoa(ids[0])+`" id="sig`+itoa(ids[0])+`" checked`) {
		t.Fatalf("отмеченный сигнал без checked:\n%s", b)
	}

	// API для графиков
	var got []struct {
		Name  string   `json:"name"`
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(do("GET", "/api/sets"+plc, nil).Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Гидравлика" || strings.Join(got[0].Names, ",") != "Pressure,OilTemp" {
		t.Fatalf("/api/sets: %+v", got)
	}

	// Повтор имени — ошибка на той же странице, введённое не стирается
	if b := do("POST", "/sets"+plc, url.Values{"name": {"Гидравлика"}}).Body.String(); !strings.Contains(b, "уже занято") ||
		!strings.Contains(b, `value="Гидравлика"`) {
		t.Fatalf("повтор имени:\n%s", b)
	}

	// Удалить
	if w = do("POST", setPath+"/delete"+plc, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("удаление: код %d", w.Code)
	}
	if b := do("GET", "/api/sets"+plc, nil).Body.String(); strings.TrimSpace(b) != "[]" {
		t.Fatalf("после удаления /api/sets = %s, ждали []", b)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Ошибка при сохранении (занятое имя) — галочки остаются такими, как их отметили
func TestSetSaveKeepsChecks(t *testing.T) {
	w := httptest.NewRecorder()
	render(w, "set", setsView{
		PLC: store.PLC{ID: 1}, Set: store.SignalSet{ID: 2, Name: "Стол"}, Err: "имя занято",
		Signals: markRows([]store.SignalRow{{ID: 7, Name: "A"}, {ID: 8, Name: "B"}}, []int64{8}),
	})
	b := strings.Join(strings.Fields(w.Body.String()), " ")
	if !strings.Contains(b, `value="8" id="sig8" checked`) || strings.Contains(b, `value="7" id="sig7" checked`) {
		t.Fatalf("отметки:\n%s", b)
	}
}

// «История»: кнопки наборов в выборе сигналов; имена — JS-массивом, экранированы
func TestPickSetButtons(t *testing.T) {
	w := httptest.NewRecorder()
	render(w, "pick", map[string]any{
		"PLC": int64(1), "Rel": "x.pla", "Signals": []sigView{{Name: "P"}},
		"Sets": []store.SignalSet{{ID: 4, Name: "Стол", Names: []string{"P", `Q"</script>`}}},
	})
	b := w.Body.String()
	if !strings.Contains(b, ">Стол</button>") || !strings.Contains(b, `name="set"`) {
		t.Fatalf("нет кнопки набора или поля set:\n%s", b)
	}
	// html/template сам экранирует данные внутри onclick: кавычка и </script> не ломают страницу
	if strings.Contains(b, `Q"</script>`) {
		t.Fatalf("имя сигнала не экранировано:\n%s", b)
	}
}
