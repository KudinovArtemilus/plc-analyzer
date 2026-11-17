package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

// Участки на странице «ПЛК»: создать, ошибки, участок у ПЛК, переименовать, порядок, удалить;
// с чужого ПК — только смотреть
func TestAreasPage(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pid, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "Станок", IP: "192.168.0.1", Slot: 2})
	pl, _ := st.PLC(ctx, pid)
	h := (&Server{Store: st, PLC: pl}).Handler()

	do := func(method, target, from string, form url.Values) (*httptest.ResponseRecorder, string) {
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, target, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = from + ":5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w, w.Body.String()
	}
	const local, remote = "127.0.0.1", "192.0.2.50"

	// Создать: удачно — страница перерисуется; повтор и пустое — причина в блоке
	if w, _ := do("POST", "/ui/areas", local, url.Values{"name": {"Мехобработка"}}); w.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("создать участок: %d %s", w.Code, w.Body.String())
	}
	do("POST", "/ui/areas", local, url.Values{"name": {"Прессовый"}})
	if _, b := do("POST", "/ui/areas", local, url.Values{"name": {"Прессовый"}}); !strings.Contains(b, "уже есть") || !strings.Contains(b, `value="Прессовый"`) {
		t.Errorf("повтор: %s", b)
	}
	if _, b := do("POST", "/ui/areas", local, url.Values{"name": {"  "}}); !strings.Contains(b, "название участка — от 1 до") {
		t.Errorf("пустое: %s", b)
	}
	areas, _ := st.Areas(ctx)
	mech, press := areas[0], areas[1]

	// Страница «ПЛК»: блок участков, столбец «Участок» (у ПЛК пока «—»), выбор в форме
	if _, b := do("GET", "/plcs", local, nil); !strings.Contains(b, "<b>Мехобработка</b>") || !strings.Contains(b, "<th>Участок</th>") ||
		!strings.Contains(b, `<span class="muted">—</span>`) || !strings.Contains(b, `name="area"`) {
		t.Error("страница «ПЛК»: нет участков, столбца или выбора участка")
	}

	// Участок у ПЛК — через «изменить»: форма с выбором, сохранение, строка с названием
	if _, b := do("GET", fmt.Sprintf("/ui/plcs/%d/edit", pid), local, nil); !strings.Contains(b, fmt.Sprintf(`<option value="%d" >Прессовый</option>`, press.ID)) {
		t.Errorf("форма правки ПЛК без выбора участка: %s", b)
	}
	form := url.Values{"name": {"Станок"}, "ip": {"192.168.0.1"}, "rack": {"0"}, "slot": {"2"}, "pdu": {"240"}, "area": {fmt.Sprint(mech.ID)}}
	if _, b := do("PUT", fmt.Sprintf("/ui/plcs/%d", pid), local, form); !strings.Contains(b, "<td>Мехобработка</td>") {
		t.Errorf("строка ПЛК после выбора участка: %s", b)
	}
	if p, _ := st.PLC(ctx, pid); p.AreaID != mech.ID {
		t.Errorf("в базе участок %d", p.AreaID)
	}

	// Переименовать: строка с полем, сохранение; у ПЛК — новое название
	if _, b := do("GET", fmt.Sprintf("/ui/areas?edit=%d", mech.ID), local, nil); !strings.Contains(b, `value="Мехобработка"`) || !strings.Contains(b, "сохранить") {
		t.Errorf("режим переименования: %s", b)
	}
	if _, b := do("PUT", fmt.Sprintf("/ui/areas/%d", mech.ID), local, url.Values{"name": {""}}); !strings.Contains(b, "сохранить") {
		t.Error("ошибка переименования — строка должна остаться в режиме правки")
	}
	do("PUT", fmt.Sprintf("/ui/areas/%d", mech.ID), local, url.Values{"name": {"Механообработка"}})
	if p, _ := st.PLC(ctx, pid); p.Area != "Механообработка" {
		t.Errorf("после переименования: %q", p.Area)
	}

	// Порядок
	do("POST", fmt.Sprintf("/ui/areas/%d/move?d=-1", press.ID), local, nil)
	if list, _ := st.Areas(ctx); list[0].Name != "Прессовый" {
		t.Errorf("порядок: %v", list)
	}

	// С чужого ПК: участки видны текстом, менять нельзя
	if _, b := do("GET", "/plcs", remote, nil); !strings.Contains(b, "<b>Механообработка</b>") {
		t.Error("с чужого ПК участки должны быть видны")
	}
	for _, tc := range []struct{ method, target string }{
		{"POST", "/ui/areas"}, {"PUT", fmt.Sprintf("/ui/areas/%d", press.ID)},
		{"POST", fmt.Sprintf("/ui/areas/%d/move?d=1", press.ID)}, {"DELETE", fmt.Sprintf("/ui/areas/%d", press.ID)},
	} {
		if w, _ := do(tc.method, tc.target, remote, url.Values{"name": {"Взлом"}}); w.Code != 403 {
			t.Errorf("с чужого ПК %s %s: код %d", tc.method, tc.target, w.Code)
		}
	}

	// Удалить участок: ПЛК остаётся, без участка
	do("DELETE", fmt.Sprintf("/ui/areas/%d", mech.ID), local, nil)
	if p, err := st.PLC(ctx, pid); err != nil || p.AreaID != 0 {
		t.Errorf("ПЛК после удаления участка: %v %d", err, p.AreaID)
	}
}
