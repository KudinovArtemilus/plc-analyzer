package web

import (
	"net"
	"net/http"
	"strings"
)

// ---------- Доступ из сети: смотреть можно всем, менять — только своим ----------
//
// Чтение (GET, HEAD) разрешено любому компьютеру, который достучался до анализатора.
// Изменения (POST, PUT, DELETE: сигналы, ПЛК, нормы, справочники, шунты, слепки) —
// только с этого ПК (127.0.0.1) и с адресов из флага -admin.
// Проверка на сервере, а не в браузере: кнопку можно спрятать, но запрос всё равно
// можно отправить вручную. Защищает сервер.

// ParseAdmins — список адресов из флага -admin: "10.0.0.5, 10.0.0.6" → набор.
// Неверный адрес возвращается вторым значением — чтобы программа сказала об опечатке при запуске.
func ParseAdmins(list string) (map[string]bool, []string) {
	out := map[string]bool{}
	var bad []string
	for _, a := range strings.Split(list, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		ip := net.ParseIP(a)
		if ip == nil {
			bad = append(bad, a)
			continue
		}
		out[ip.String()] = true // к одному виду: «010.0.0.5» и «10.0.0.5» — один адрес
	}
	return out, bad
}

// canEdit — можно ли этому запросу что-то менять
func (s *Server) canEdit(r *http.Request) bool {
	ip := net.ParseIP(s.clientIP(r))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || s.Admins[ip.String()]
}

// ---------- За посредником (Caddy): чей это запрос на самом деле ----------
//
// Когда перед анализатором стоит посредник с паролем и HTTPS, анализатор слушает только
// 127.0.0.1, и ВСЕ запросы приходят к нему с 127.0.0.1 — от посредника. Без этой функции
// любой, кто ввёл пароль, считался бы «этим ПК» и мог бы менять настройки.
// Настоящий адрес посредник пишет в заголовок X-Forwarded-For.
//
// Верим заголовку, только если включено -proxy И запрос пришёл с этого же ПК: подделать
// заголовок мог бы любой, но достучаться до 127.0.0.1 может только посредник (и сам этот ПК).

// remoteHost — адрес из соединения, без порта
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedFor — адрес клиента от посредника: последний в X-Forwarded-For (его дописал
// ближайший посредник — наш). "" — заголовка нет или запрос не с этого ПК.
func forwardedFor(r *http.Request) string {
	ip := net.ParseIP(remoteHost(r))
	xff := r.Header.Get("X-Forwarded-For")
	if ip == nil || !ip.IsLoopback() || xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// clientIP — адрес того, кто на самом деле прислал запрос
func (s *Server) clientIP(r *http.Request) string {
	if s.Proxy {
		if f := forwardedFor(r); f != "" {
			return f
		}
	}
	return remoteHost(r)
}

// readOnlyMsg — что видит человек за чужим ПК, нажав «изменить»
const readOnlyMsg = "Только просмотр: изменять настройки можно на ПК анализатора " +
	"(или с адреса, добавленного флагом -admin)"

// guard пропускает чтение всем, а изменения — только canEdit
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || s.canEdit(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Кнопки на страницах — HTMX. Ответ с ошибкой HTMX молча не показывает,
		// поэтому для него — 200 и полоса с объяснением вверху страницы, ничего не меняя.
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Retarget", "body")
			w.Header().Set("HX-Reswap", "afterbegin")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<div class="bar bad">` + readOnlyMsg + `</div>`))
			return
		}
		http.Error(w, readOnlyMsg, http.StatusForbidden)
	})
}

// ---------- Прятать кнопки правки у тех, кому нельзя ----------
//
// Каждая страница подключает /access.css. Для своих (canEdit) файл пустой,
// для остальных — одно правило, которое прячет всё, что меняет настройки.
// Так решает СЕРВЕР по тому же canEdit, что и guard: правило одно, расхождений нет.
// И это CSS, а не шаблоны: правило действует и на строки, которые HTMX
// подставит позже (например, после «показать ещё»).

// readOnlyCSS — что прятать:
//   - [hx-post] [hx-put] [hx-delete] и form[method=post] — всё, что отправляет изменение
//     (те же методы, которые не пропускает guard);
//   - .edit — кнопки, которые только ОТКРЫВАЮТ форму правки («изменить», «шунт», «Слепок»):
//     сами они делают GET, по атрибутам их не отличить, поэтому помечены классом.
const readOnlyCSS = `[hx-post], [hx-put], [hx-delete], form[method="post"], .edit {
	display: none !important;
}
`

// accessCSS — GET /access.css
func (s *Server) accessCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache") // права могут поменяться при перезапуске с другим -admin
	if !s.canEdit(r) {
		w.Write([]byte(readOnlyCSS))
	}
}
