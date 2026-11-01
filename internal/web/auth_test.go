package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("смена-2026")
	if err != nil || !strings.HasPrefix(h, "pbkdf2-sha256$600000$") {
		t.Fatalf("отпечаток %q, %v", h, err)
	}
	if h2, _ := HashPassword("смена-2026"); h2 == h {
		t.Error("одинаковые пароли дали одинаковые отпечатки — соль не работает")
	}
	if ok, err := checkPassword(h, "смена-2026"); !ok || err != nil {
		t.Errorf("верный пароль не подошёл: %v", err)
	}
	if ok, _ := checkPassword(h, "смена-2027"); ok {
		t.Error("неверный пароль подошёл")
	}
	if _, err := HashPassword("12345"); err != ErrShortPassword {
		t.Errorf("короткий пароль: %v", err)
	}
	if _, err := checkPassword("мусор", "x"); err == nil {
		t.Error("испорченный отпечаток должен давать ошибку")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/history?plc=1": "/history?plc=1", "": "/", "http://evil": "/", "//evil.com": "/", "/\\evil": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plcID, _ := st.AddPLC(ctx, "тест", store.PLC{Name: "SGGT", IP: "192.168.0.1", Slot: 2})
	pl, _ := st.PLC(ctx, plcID)
	h := (&Server{Store: st, PLC: pl}).Handler()

	do := func(method, target, from, cookie string, form url.Values) *httptest.ResponseRecorder {
		var r *http.Request
		if form != nil {
			r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		r.RemoteAddr = from + ":5000"
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const remote = "10.0.0.99"

	// Пароль не задан — всё открыто, как раньше
	if w := do("GET", "/plcs", remote, "", nil); w.Code != 200 {
		t.Fatalf("без пароля: код %d", w.Code)
	}

	hash, _ := HashPassword("смена-2026")
	st.SetSetting(ctx, "тест", pwKey, hash, "пароль задан")

	// Чужой ПК: страница → на вход (и потом обратно), данные → 401
	w := do("GET", "/history?plc=1", remote, "", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?next="+url.QueryEscape("/history?plc=1") {
		t.Fatalf("страница без входа: код %d, %q", w.Code, w.Header().Get("Location"))
	}
	if w := do("GET", "/api/plcs", remote, "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("данные без входа: код %d", w.Code)
	}
	if w := do("GET", "/login", remote, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `type="password"`) {
		t.Errorf("страница входа: код %d", w.Code)
	}
	// Сам ПК анализатора — без пароля
	if w := do("GET", "/plcs", "127.0.0.1", "", nil); w.Code != 200 {
		t.Errorf("этот ПК: код %d", w.Code)
	}

	// Неверный пароль — нет; 5 раз подряд — минута блокировки, даже для верного
	for i := 0; i < maxFails; i++ {
		if w := do("POST", "/login", remote, "", url.Values{"password": {"не тот"}}); w.Code != http.StatusUnauthorized {
			t.Fatalf("неверный пароль: код %d", w.Code)
		}
	}
	if w := do("POST", "/login", remote, "", url.Values{"password": {"смена-2026"}}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("после %d ошибок: код %d", maxFails, w.Code)
	}

	// С другого ПК (не заблокированного) — верный пароль: cookie и возврат на страницу
	w = do("POST", "/login", "10.0.0.98", "", url.Values{"password": {"смена-2026"}, "next": {"/history?plc=1"}})
	var tok string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			tok = c.Value
			if !c.HttpOnly {
				t.Error("cookie должна быть HttpOnly")
			}
		}
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/history?plc=1" || tok == "" {
		t.Fatalf("вход: код %d, %q, токен %q", w.Code, w.Header().Get("Location"), tok)
	}
	if w := do("GET", "/plcs", "10.0.0.98", tok, nil); w.Code != 200 {
		t.Errorf("после входа: код %d", w.Code)
	}
	// Вход не даёт права менять: это по-прежнему только ПК анализатора
	if w := do("POST", "/sets?plc=1", "10.0.0.98", tok, url.Values{"name": {"Гидравлика"}}); w.Code != http.StatusForbidden {
		t.Errorf("изменение после входа с чужого ПК: код %d", w.Code)
	}
	// Поддельный токен не подходит; после выхода токен больше не действует
	if w := do("GET", "/plcs", "10.0.0.98", "fake-token", nil); w.Code != http.StatusSeeOther {
		t.Errorf("поддельный токен: код %d", w.Code)
	}
	do("GET", "/logout", "10.0.0.98", tok, nil)
	if w := do("GET", "/plcs", "10.0.0.98", tok, nil); w.Code != http.StatusSeeOther {
		t.Errorf("после выхода: код %d", w.Code)
	}
}
