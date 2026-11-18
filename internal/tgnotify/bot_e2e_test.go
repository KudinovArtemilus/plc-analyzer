package tgnotify

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgbot"
)

// Весь путь, как в службе: авария в журнале → Watcher → бот → «Telegram» (свой тестовый)
func TestEndToEnd(t *testing.T) {
	st, ctx, p1, _ := setup(t)
	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		result := any(true)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			result = map[string]string{"username": "test_bot"}
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			result = []any{}
			time.Sleep(50 * time.Millisecond)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			mu.Lock()
			sent = append(sent, p["text"].(string))
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer tg.Close()
	log.SetOutput(io.Discard)

	w := New(st)
	bot := tgbot.New(tgbot.Deps{API: tg.URL, Idle: 10 * time.Millisecond,
		Load: func(context.Context) (tgbot.Config, error) {
			return tgbot.Config{Token: "123456789:TEST_TOKEN_abcdefghijklmnopqrstuvwxyz", Users: []int64{111}, Page: "https://x/", Period: 60}, nil
		},
		Summary: func(context.Context) (summary.Summary, error) { return summary.Summary{Period: 60}, nil },
		Notes: func(ctx context.Context) ([]tgbot.Note, error) {
			ns, err := w.Notes(ctx, time.Now())
			out := make([]tgbot.Note, len(ns))
			for i, n := range ns {
				out[i] = tgbot.Note{Text: n.Text, Silent: n.Silent}
			}
			return out, err
		}})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { bot.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(200 * time.Millisecond) // бот запустился и запомнил, где журнал
	st.SaveEdges(ctx, p1, []store.EventEdge{edge("Дверь", "Дверь открыта", true, time.Now())})

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != "🔴 Шлифовка — Дверь открыта" {
		t.Fatalf("в Telegram: %q", sent)
	}
}
