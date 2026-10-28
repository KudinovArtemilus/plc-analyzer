package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAdmins(t *testing.T) {
	got, bad := ParseAdmins(" 10.0.0.5, ,10.0.0.6,10.0.0.x")
	if !got["10.0.0.5"] || !got["10.0.0.6"] || len(got) != 2 {
		t.Fatalf("адреса: %v", got)
	}
	if len(bad) != 1 || bad[0] != "10.0.0.x" {
		t.Fatalf("опечатка должна попасть в bad: %v", bad)
	}
}

func TestGuard(t *testing.T) {
	s := &Server{Admins: map[string]bool{"10.0.0.5": true}}
	called := false
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	do := func(method, from string, htmx bool) *httptest.ResponseRecorder {
		called = false
		r := httptest.NewRequest(method, "/ui/signals/1", nil)
		r.RemoteAddr = from + ":51000"
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Смотреть можно всем
	if do("GET", "10.0.0.99", false); !called {
		t.Error("GET с чужого ПК должен проходить")
	}
	// Менять — этому ПК и админу
	if do("DELETE", "127.0.0.1", true); !called {
		t.Error("с этого ПК изменения должны проходить")
	}
	if do("POST", "10.0.0.5", false); !called {
		t.Error("с адреса из -admin изменения должны проходить")
	}
	// Чужой ПК: обычная форма — 403
	if w := do("POST", "10.0.0.99", false); called || w.Code != http.StatusForbidden {
		t.Errorf("POST с чужого ПК: обработчик вызван %v, код %d", called, w.Code)
	}
	// Чужой ПК, кнопка HTMX — ничего не меняется, полоса с объяснением вверху страницы
	w := do("PUT", "10.0.0.99", true)
	if called || w.Header().Get("HX-Retarget") != "body" || !strings.Contains(w.Body.String(), "Только просмотр") {
		t.Errorf("HTMX с чужого ПК: вызван %v, заголовки %v, тело %q", called, w.Header(), w.Body.String())
	}
}

func TestAccessCSS(t *testing.T) {
	s := &Server{Admins: map[string]bool{"10.0.0.5": true}}
	get := func(from string) string {
		r := httptest.NewRequest("GET", "/access.css", nil)
		r.RemoteAddr = from + ":51000"
		w := httptest.NewRecorder()
		s.accessCSS(w, r)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
			t.Errorf("%s: Content-Type %q — браузер не применит стиль", from, ct)
		}
		return w.Body.String()
	}
	for _, own := range []string{"127.0.0.1", "10.0.0.5"} {
		if css := get(own); css != "" {
			t.Errorf("%s может менять — кнопки прятать нельзя, а пришло %q", own, css)
		}
	}
	if css := get("10.0.0.99"); !strings.Contains(css, "display: none") || !strings.Contains(css, ".edit") {
		t.Errorf("чужой ПК — кнопки должны прятаться, а пришло %q", css)
	}
}
