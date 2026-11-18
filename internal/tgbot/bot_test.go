package tgbot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
)

const token = "123456789:TEST_TOKEN_abcdefghijklmnopqrstuvwxyz"

// fakeTelegram — свой «Telegram»: отдаёт сообщения из очереди и запоминает, что бот отправил
type fakeTelegram struct {
	mu      sync.Mutex
	updates []map[string]any
	calls   []string         // метод: параметры
	sent    []map[string]any // sendMessage
	menus   []map[string]any // setChatMenuButton
	fail    string           // ответить ошибкой на этот метод
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/") // /bot<token>/<method>
	method := parts[len(parts)-1]
	var p map[string]any
	json.NewDecoder(r.Body).Decode(&p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
	if parts[1] != "bot"+token {
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
		return
	}
	if method == f.fail {
		w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
		return
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"username": "Status_test_bot"}
	case "getUpdates":
		result = f.updates
		f.updates = nil
		if len(result.([]map[string]any)) == 0 {
			result = []any{}
			time.Sleep(20 * time.Millisecond) // «ждём сообщений»
		}
	case "sendMessage":
		f.sent = append(f.sent, p)
	case "setChatMenuButton":
		f.menus = append(f.menus, p)
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func msg(id int, from int64, chatType, text string) map[string]any {
	return map[string]any{"update_id": id, "message": map[string]any{
		"chat": map[string]any{"id": from, "type": chatType}, "from": map[string]any{"id": from}, "text": text}}
}

var demo = summary.Summary{At: "2026-11-17T10:00:00+05:00", Period: 60, Areas: []summary.Area{{Name: "Мехобработка",
	Machines: []summary.Machine{{Name: "Шлифовка", Online: true, State: &summary.State{Name: "Автомат", Color: "blue"}}}}}}

// startBot — бот против своего «Telegram»; вернёт, когда условие выполнится (или упадёт по таймауту)
func startBot(t *testing.T, f *fakeTelegram, cfg Config, until func() bool) *Bot {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	builds := 0
	b := New(Deps{API: srv.URL, Idle: 10 * time.Millisecond,
		Load:    func(context.Context) (Config, error) { return cfg, nil },
		Summary: func(context.Context) (summary.Summary, error) { builds++; return demo, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		ok := until()
		f.mu.Unlock()
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	return b
}

func TestBotAnswersOnlyUsers(t *testing.T) {
	f := &fakeTelegram{updates: []map[string]any{
		msg(1, 111, "private", "/start"),  // получатель
		msg(2, 999, "private", "/status"), // чужой
		msg(3, -100, "group", "/status"),  // группа
		msg(4, 999, "private", "ещё раз"), // чужой снова — в лог не дублируется
	}}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(io.Discard)

	cfg := Config{Token: token, Users: []int64{111, 222}, Page: "https://example.github.io/tg/", Period: 60}
	b := startBot(t, f, cfg, func() bool { return len(f.sent) > 0 && len(f.menus) >= 3 })

	if len(f.sent) != 1 || f.sent[0]["chat_id"].(float64) != 111 || !strings.Contains(f.sent[0]["text"].(string), "Шлифовка — Автомат") {
		t.Fatalf("ответы: %+v", f.sent)
	}
	// «Табло» — обоим получателям (по расписанию) и ещё раз тому, кто написал
	got := map[float64]int{}
	for _, m := range f.menus {
		got[m["chat_id"].(float64)]++
	}
	if got[111] < 2 || got[222] < 1 || got[999] != 0 {
		t.Errorf("кнопки «Табло»: %v", got)
	}
	if n := strings.Count(logs.String(), "чужой ID 999"); n != 1 {
		t.Errorf("про чужого в логе %d раз:\n%s", n, logs.String())
	}
	st := b.Status()
	if !st.Configured || st.Bot != "@Status_test_bot" || st.Users != 2 || st.Pushed.IsZero() {
		t.Errorf("состояние: %+v", st)
	}
}

// Ссылка «Табло»: сводка после #, читается обратно; слишком длинная — ошибка
func TestLink(t *testing.T) {
	link, err := Link("https://example.github.io/tg/", demo)
	if err != nil {
		t.Fatal(err)
	}
	pre := "https://example.github.io/tg/#d="
	if !strings.HasPrefix(link, pre) {
		t.Fatalf("ссылка: %s", link)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, pre))
	var back summary.Summary
	if err != nil || json.Unmarshal(raw, &back) != nil || back.Areas[0].Machines[0].Name != "Шлифовка" {
		t.Errorf("сводка из ссылки не читается: %v %s", err, raw)
	}
	big := demo
	big.Areas = append([]summary.Area{}, demo.Areas...)
	big.Areas[0].Name = strings.Repeat("я", MaxLink)
	if _, err := Link("https://x/", big); err == nil || !strings.Contains(err.Error(), "не помещается") {
		t.Errorf("длинная сводка: %v", err)
	}
}

// Токен не попадает ни в ошибки, ни в состояние для страницы, ни в лог
func TestTokenHidden(t *testing.T) {
	f := &fakeTelegram{fail: "setChatMenuButton"}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(io.Discard)
	b := startBot(t, f, Config{Token: token, Users: []int64{111}, Page: "https://x/", Period: 60},
		func() bool { return len(f.calls) >= 3 })
	st := b.Status()
	if !strings.Contains(st.Err, "chat not found") {
		t.Errorf("ошибка «Табло» не видна: %+v", st)
	}
	all := logs.String() + st.Err + st.Bot
	if strings.Contains(all, token) || strings.Contains(all, "TEST_TOKEN") {
		t.Errorf("токен утёк: %s", all)
	}
	// Ошибка сети с токеном в адресе — тоже прячется
	bad := New(Deps{API: "http://127.0.0.1:1"})
	err := bad.call(context.Background(), token, "getMe", nil, nil)
	if err == nil || strings.Contains(err.Error(), "TEST_TOKEN") {
		t.Errorf("ошибка сети: %v", err)
	}
}

// Токена нет — бот молчит и Telegram не трогает
func TestNoToken(t *testing.T) {
	f := &fakeTelegram{}
	start := time.Now()
	b := startBot(t, f, Config{}, func() bool { return time.Since(start) > 100*time.Millisecond })
	if len(f.calls) != 0 || b.Status().Configured {
		t.Errorf("без токена: вызовы %v, состояние %+v", f.calls, b.Status())
	}
}
