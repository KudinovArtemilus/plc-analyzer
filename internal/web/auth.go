package web

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ---------- Вход по паролю для просмотра (37) ----------
//
// На самом ПК анализатора — без пароля, как раньше. С любого другого ПК сначала страница
// входа: знаешь пароль — смотришь. Менять настройки по-прежнему можно только с ПК
// анализатора (и адресов из -admin) — это решает guard, пароль на это не влияет.
//
// Пароль не задан (plc-analyzer.exe password ещё не выполняли) — вход не нужен, всё как раньше.
//
// После входа браузер получает cookie на смену (12 ч). Сессии живут в памяти программы:
// после перезапуска службы войти нужно заново — так проще и безопаснее, чем хранить их в базе.

const (
	sessionCookie = "plca_session"
	sessionTTL    = 12 * time.Hour
	maxFails      = 5           // неверных паролей подряд с одного адреса…
	blockFor      = time.Minute // …и вход с него закрыт на минуту: подбор становится бессмысленным
)

type failInfo struct {
	n     int
	until time.Time
}

// sessions — открытые входы и неудачные попытки. Нулевое значение готово к работе.
type sessions struct {
	mu    sync.Mutex
	open  map[string]time.Time // токен → до когда действует
	fails map[string]failInfo  // адрес → неудачные попытки
}

func (ss *sessions) create() (string, error) {
	b := make([]byte, 32) // 256 бит случайности: угадать чужой токен невозможно
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.open == nil {
		ss.open = map[string]time.Time{}
	}
	now := time.Now()
	for t, until := range ss.open { // заодно выбросить истёкшие
		if now.After(until) {
			delete(ss.open, t)
		}
	}
	ss.open[tok] = now.Add(sessionTTL)
	return tok, nil
}

func (ss *sessions) valid(tok string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	until, ok := ss.open[tok]
	return ok && time.Now().Before(until)
}

func (ss *sessions) drop(tok string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.open, tok)
}

// blocked — адрес временно не пускаем после серии неверных паролей
func (ss *sessions) blocked(ip string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return time.Now().Before(ss.fails[ip].until)
}

func (ss *sessions) fail(ip string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.fails == nil {
		ss.fails = map[string]failInfo{}
	}
	f := ss.fails[ip]
	f.n++
	if f.n >= maxFails {
		f = failInfo{until: time.Now().Add(blockFor)} // счёт заново после блокировки
	}
	ss.fails[ip] = f
}

func (ss *sessions) success(ip string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.fails, ip)
}

// openPaths — что доступно без входа: сама страница входа и её стили
var openPaths = map[string]bool{"/login": true, "/isa101.css": true, "/access.css": true}

// requireLogin пускает: этот ПК — всегда; остальных — если пароль не задан или есть вход
func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if openPaths[r.URL.Path] || s.isLocal(r) {
			next.ServeHTTP(w, r)
			return
		}
		hash, err := s.Store.Setting(r.Context(), pwKey)
		if err != nil {
			http.Error(w, "база: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if hash == "" {
			next.ServeHTTP(w, r) // пароль не задан — вход не нужен
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil && s.auth.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		// Страницу — на вход (и потом обратно); данным, потоку Live и HTMX — просто «нужен вход»
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") &&
			!strings.HasPrefix(r.URL.Path, "/ui/") && r.Header.Get("HX-Request") == "" {
			http.Redirect(w, r, "/login?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		http.Error(w, "нужен вход: откройте страницу заново", http.StatusUnauthorized)
	})
}

// isLocal — запрос с самого ПК анализатора
func (s *Server) isLocal(r *http.Request) bool {
	ip := net.ParseIP(s.clientIP(r))
	return ip != nil && ip.IsLoopback()
}

type loginView struct {
	Next, Err string
}

// safeNext — куда вернуть после входа: только путь этого же сайта, не «//чужой.сайт»
func safeNext(n string) string {
	if !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") {
		return "/"
	}
	return n
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, "login", loginView{Next: safeNext(r.URL.Query().Get("next"))})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	v := loginView{Next: safeNext(r.PostForm.Get("next"))}
	ip := s.clientIP(r)
	if s.auth.blocked(ip) {
		v.Err = "Слишком много неверных попыток — подождите минуту."
		w.WriteHeader(http.StatusTooManyRequests)
		render(w, "login", v)
		return
	}
	hash, err := s.Store.Setting(r.Context(), pwKey)
	if err != nil {
		http.Error(w, "база: "+err.Error(), http.StatusInternalServerError)
		return
	}
	ok := hash == "" // пароля нет — пускаем (страница входа не нужна, но и не ломается)
	if !ok {
		if ok, err = checkPassword(hash, r.PostForm.Get("password")); err != nil {
			log.Printf("вход: %v", err)
		}
	}
	if !ok {
		s.auth.fail(ip)
		log.Printf("вход: неверный пароль с %s", ip)
		v.Err = "Неверный пароль."
		w.WriteHeader(http.StatusUnauthorized)
		render(w, "login", v)
		return
	}
	s.auth.success(ip)
	tok, err := s.auth.create()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,                 // скрипты страницы токен не видят
		SameSite: http.SameSiteLaxMode, // чужой сайт не отправит запрос «от имени» вошедшего
		Secure:   r.TLS != nil,         // по HTTPS (37b) — только по HTTPS
	})
	log.Printf("вход: %s", ip)
	http.Redirect(w, r, v.Next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }
