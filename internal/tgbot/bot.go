// Package tgbot — бот Telegram прямо на анализаторе (шаг 45b).
//
// Никаких входящих соединений: бот сам спрашивает Telegram «есть новые сообщения?»
// (long polling, getUpdates) и сам отправляет ответы. Для заводской сети это обычные
// исходящие HTTPS-запросы к api.telegram.org, как открыть сайт в браузере.
//
// Что делает:
//   - раз в период ставит каждому получателю кнопку меню «Табло» со свежей сводкой в ссылке
//     (после #, браузер не отправляет эту часть на сервер, GitHub Pages сводку не видит);
//   - на любое сообщение от получателя отвечает сводкой текстом (/status);
//   - чужим не отвечает ничего; в лог — один раз «пишет чужой ID N» (чтобы его можно было добавить).
//
// Бот только показывает: команд, которые что-то меняют, нет и не будет.
// Ошибки бота не трогают опрос и запись — это отдельная горутина со своими паузами.
package tgbot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
)

// MinPeriod — чаще раза в минуту «Табло» не обновляем: Telegram ограничивает setChatMenuButton
// (при обновлении раз в 10 с ответил «Too Many Requests: retry after 162»)
const MinPeriod = 60

// MaxLink — самая длинная ссылка «Табло», проверенная пробой (Desktop и Android: 8000 символов данных)
const MaxLink = 8000

// Config — то, что бот берёт из базы перед каждым циклом (меняется без перезапуска службы)
type Config struct {
	Token  string
	Users  []int64
	Page   string // адрес табло, к нему дописывается #d=<сводка>
	Period int    // как часто обновлять «Табло», с
}

// Deps — откуда бот берёт настройки и сводку
type Deps struct {
	Load    func(ctx context.Context) (Config, error)
	Summary func(ctx context.Context) (summary.Summary, error)
	Notes   func(ctx context.Context) ([]Note, error) // уведомления об авариях (45d); nil — нет
	API     string                                    // "" — https://api.telegram.org (в тестах — свой сервер)
	Idle    time.Duration                             // пауза, если бот не настроен или Telegram недоступен; 0 — 30 с
}

// Status — для страницы «Telegram»: что с ботом. Токена здесь нет.
type Status struct {
	Configured bool      // токен задан
	Bot        string    // @имя бота (если Telegram ответил)
	Users      int       // получателей
	Pushed     time.Time // когда последний раз обновили «Табло»
	Bytes      int       // длина ссылки «Табло»
	Err        string    // последняя ошибка (пусто — всё хорошо)
	ErrAt      time.Time
}

type Bot struct {
	d    Deps
	http *http.Client

	mu sync.Mutex
	st Status

	token     string    // для какого токена узнали имя бота
	holdUntil time.Time // Telegram попросил подождать («Too Many Requests») — до этого времени «Табло» не трогаем
	noteHold  time.Time // то же для уведомлений
	outbox    []Note    // уведомления, которые ещё не ушли (Telegram недоступен или просил подождать)
	strangers map[int64]bool
}

func New(d Deps) *Bot {
	if d.API == "" {
		d.API = "https://api.telegram.org"
	}
	if d.Idle == 0 {
		d.Idle = 30 * time.Second
	}
	return &Bot{d: d, http: &http.Client{Timeout: 60 * time.Second}, strangers: map[int64]bool{}}
}

// Status — копия состояния (безопасно из любой горутины)
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st
}

func (b *Bot) set(f func(*Status)) {
	b.mu.Lock()
	f(&b.st)
	b.mu.Unlock()
}

// fail — запомнить ошибку и написать в лог, только если она новая (не засорять лог раз в минуту)
func (b *Bot) fail(err error) {
	b.mu.Lock()
	same := b.st.Err == err.Error()
	b.st.Err, b.st.ErrAt = err.Error(), time.Now()
	b.mu.Unlock()
	if !same {
		log.Printf("telegram: %v", err)
	}
}

func (b *Bot) ok() { b.set(func(s *Status) { s.Err = "" }) }

// Run — работа бота до остановки программы
func (b *Bot) Run(ctx context.Context) {
	var next time.Time // когда снова обновлять «Табло»
	offset := 0
	for ctx.Err() == nil {
		cfg, err := b.d.Load(ctx)
		if err != nil || cfg.Token == "" {
			b.set(func(s *Status) { *s = Status{} })
			b.token = ""
			sleep(ctx, b.d.Idle)
			continue
		}
		b.set(func(s *Status) { s.Configured, s.Users = true, len(cfg.Users) })

		if cfg.Token != b.token { // новый токен — узнать, чей он
			var me struct{ Username string }
			if err := b.call(ctx, cfg.Token, "getMe", nil, &me); err != nil {
				b.fail(err)
				sleep(ctx, b.d.Idle)
				continue
			}
			b.token, next = cfg.Token, time.Time{}
			b.set(func(s *Status) { s.Bot = "@" + me.Username })
			log.Printf("telegram: бот @%s на связи, получателей %d", me.Username, len(cfg.Users))
		}

		// Уведомления об авариях — каждый круг (не реже 5 с), не дожидаясь «Табло»
		if b.d.Notes != nil {
			notes, err := b.d.Notes(ctx)
			if err != nil {
				b.fail(fmt.Errorf("уведомления: %w", err))
			}
			b.outbox = append(b.outbox, notes...)
			b.sendNotes(ctx, cfg)
		}

		// Сводка читает файлы смены — собираем её, только когда она нужна:
		// пора обновить «Табло» или кто-то из получателей написал
		if !time.Now().Before(next) {
			next = time.Now().Add(time.Duration(max(cfg.Period, MinPeriod)) * time.Second)
			if sum, err := b.d.Summary(ctx); err != nil {
				b.fail(fmt.Errorf("сводка: %w", err))
			} else {
				b.push(ctx, cfg, sum)
			}
		}

		// Ждём сообщений до следующего обновления «Табло» (Telegram держит запрос открытым)
		// Не дольше 5 с: за это время может прийти авария, о которой надо сообщить
		wait := min(max(time.Until(next), time.Second), NoteEvery)
		offset, err = b.poll(ctx, cfg, offset, wait)
		if err != nil && ctx.Err() == nil { // остановка программы — не ошибка бота
			b.fail(err)
			sleep(ctx, b.d.Idle)
		}
	}
}

// push — каждому получателю кнопка меню «Табло» со сводкой в ссылке
// Note — уведомление: текст и «без звука»
type Note struct {
	Text   string
	Silent bool
}

// NoteEvery — как часто бот проверяет журнал аварий
const NoteEvery = 5 * time.Second

// OutboxMax — больше уведомлений не копим (Telegram долго недоступен): старые выбрасываем
const OutboxMax = 50

// sendNotes — разослать накопленные уведомления всем получателям.
// Не ушло (нет сети, «подождите») — останется в очереди до следующего круга.
func (b *Bot) sendNotes(ctx context.Context, cfg Config) {
	if len(b.outbox) > OutboxMax {
		b.outbox = b.outbox[len(b.outbox)-OutboxMax:]
	}
	for len(b.outbox) > 0 && !time.Now().Before(b.noteHold) {
		n := b.outbox[0]
		sent := 0
		for _, u := range cfg.Users {
			err := b.call(ctx, cfg.Token, "sendMessage", map[string]any{
				"chat_id": u, "text": n.Text, "disable_notification": n.Silent}, nil)
			var tm *TooManyError
			switch {
			case err == nil:
				sent++
			case errors.As(err, &tm):
				b.noteHold = time.Now().Add(tm.After + time.Second)
				b.fail(fmt.Errorf("уведомление: %w", err))
				return // это и следующие — позже (кто-то из получателей может получить дважды)
			case ctx.Err() == nil:
				b.fail(fmt.Errorf("уведомление для %d: %w", u, err)) // например, не нажал /start
			}
			sleep(ctx, 50*time.Millisecond) // не чаще ~20 сообщений в секунду на бота
		}
		if sent == 0 && len(cfg.Users) > 0 {
			return // не ушло никому (нет сети?) — попробуем на следующем круге
		}
		b.outbox = b.outbox[1:]
	}
}

func (b *Bot) push(ctx context.Context, cfg Config, sum summary.Summary) {
	if time.Now().Before(b.holdUntil) {
		return // Telegram просил подождать — не злим его ещё сильнее
	}
	link, err := Link(cfg.Page, sum)
	if err != nil {
		b.fail(err)
		return
	}
	var failed error
	for _, u := range cfg.Users {
		if err := b.menu(ctx, cfg.Token, u, link); err != nil {
			if b.hold(err) {
				failed = fmt.Errorf("«Табло»: %w", err)
				break // остальным — тоже нельзя: ограничение на бота, а не на одного человека
			}
			// «chat not found» — человек ещё не нажал /start у бота: Telegram не даёт писать первым
			failed = fmt.Errorf("«Табло» для %d: %w", u, err)
		}
	}
	b.set(func(s *Status) { s.Pushed, s.Bytes = time.Now(), len(link) })
	if failed != nil {
		b.fail(failed)
		return
	}
	b.ok()
}

func (b *Bot) menu(ctx context.Context, token string, user int64, link string) error {
	return b.call(ctx, token, "setChatMenuButton", map[string]any{"chat_id": user,
		"menu_button": map[string]any{"type": "web_app", "text": "Табло", "web_app": map[string]string{"url": link}}}, nil)
}

// poll — новые сообщения; получателю — сводка текстом и свежее «Табло», чужим — ничего
func (b *Bot) poll(ctx context.Context, cfg Config, offset int, wait time.Duration) (int, error) {
	var ups []struct {
		UpdateID int `json:"update_id"`
		Message  *struct {
			Chat struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
			Text string `json:"text"`
		} `json:"message"`
	}
	err := b.call(ctx, cfg.Token, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(wait.Seconds()), "allowed_updates": []string{"message"}}, &ups)
	if err != nil {
		return offset, err
	}
	var sum *summary.Summary // одна сводка на все сообщения этой пачки
	for _, u := range ups {
		offset = u.UpdateID + 1
		m := u.Message
		if m == nil || m.Chat.Type != "private" {
			continue // группы и каналы — нет: сводка только в личном чате
		}
		if !slices.Contains(cfg.Users, m.From.ID) {
			if !b.strangers[m.From.ID] {
				b.strangers[m.From.ID] = true
				log.Printf("telegram: пишет чужой ID %d — не отвечаю (добавить: plc-analyzer.exe telegram)", m.From.ID)
			}
			continue
		}
		if sum == nil {
			s, err := b.d.Summary(ctx)
			if err != nil {
				return offset, fmt.Errorf("сводка: %w", err)
			}
			sum = &s
		}
		if err := b.call(ctx, cfg.Token, "sendMessage", map[string]any{
			"chat_id": m.Chat.ID, "text": summary.Text(*sum)}, nil); err != nil {
			return offset, err
		}
		if link, err := Link(cfg.Page, *sum); err == nil && !time.Now().Before(b.holdUntil) {
			b.hold(b.menu(ctx, cfg.Token, m.From.ID, link)) // ответил — у человека сразу есть «Табло»
		}
	}
	return offset, nil
}

// Link — адрес табло со сводкой после #: base64url от JSON (только латиница, цифры, - и _)
func Link(page string, sum summary.Summary) (string, error) {
	raw, err := json.Marshal(sum)
	if err != nil {
		return "", err
	}
	link := strings.TrimRight(page, "#") + "#d=" + base64.RawURLEncoding.EncodeToString(raw)
	if len(link) > MaxLink {
		return "", fmt.Errorf("сводка не помещается в ссылку «Табло»: %d символов из %d — уберите сигналы или станки", len(link), MaxLink)
	}
	return link, nil
}

// call — метод Bot API. Токен — только в адресе запроса; в текст ошибок он не попадает.
func (b *Bot) call(ctx context.Context, token, method string, params, out any) error {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, "POST", b.d.API+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New(hide(err.Error(), token))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s", method, hide(err.Error(), token))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("%s: ответ не JSON (HTTP %d)", method, resp.StatusCode)
	}
	if !r.OK {
		if r.Parameters.RetryAfter > 0 {
			return &TooManyError{Method: method, After: time.Duration(r.Parameters.RetryAfter) * time.Second}
		}
		return fmt.Errorf("%s: %s", method, hide(r.Description, token))
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// TooManyError — Telegram: «Too Many Requests: retry after N» — слишком часто, подождите N с
type TooManyError struct {
	Method string
	After  time.Duration
}

func (e *TooManyError) Error() string {
	return fmt.Sprintf("%s: Telegram просит не так часто — пауза %v (до %s)",
		e.Method, e.After, time.Now().Add(e.After).Format("15:04:05"))
}

// hold — если Telegram просит подождать, запомнить до какого времени; true — это был такой ответ
func (b *Bot) hold(err error) bool {
	var tm *TooManyError
	if !errors.As(err, &tm) {
		return false
	}
	b.holdUntil = time.Now().Add(tm.After + time.Second)
	return true
}

func hide(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// sleep — пауза, которую прерывает остановка программы
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
