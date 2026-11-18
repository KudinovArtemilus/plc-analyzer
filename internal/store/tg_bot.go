package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ---------- Бот Telegram: токен, кому можно, адрес табло (шаг 45b) ----------
//
// Всё — в таблице settings, без новой миграции. Токен хранится как есть: боту он нужен
// для каждого запроса, захешировать его, как пароль, нельзя. Поэтому:
//   - задаётся только командой plc-analyzer.exe telegram со скрытым вводом;
//   - на страницы, в лог и в журнал изменений не попадает (в журнале — «токен задан»);
//   - база — на ПК анализатора, в GitHub её нет (.gitignore).

const (
	tgTokenKey = "tg_token"
	tgUsersKey = "tg_users" // Telegram ID через запятую
	tgPageKey  = "tg_page"  // адрес табло (GitHub Pages)

	// TGDefaultPage — табло по умолчанию: страница docs/tg/index.html на GitHub Pages
	TGDefaultPage = "https://kudinovartemilus.github.io/plc-analyzer/tg/"
)

// TGBot — настройки бота
type TGBot struct {
	Token string
	Users []int64
	Page  string
}

var (
	ErrTGToken = errors.New("это не похоже на токен бота: он выглядит как 123456789:AAH… (его выдаёт @BotFather)")
	ErrTGUsers = errors.New("Telegram ID — числа через запятую, например: 111222333, 123456789")
	ErrTGPage  = errors.New("адрес табло должен начинаться с https://")
)

var tokenRe = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)

// TGBotConfig — токен, получатели, адрес табло
func (s *Store) TGBotConfig(ctx context.Context) (TGBot, error) {
	var b TGBot
	var err error
	if b.Token, err = s.Setting(ctx, tgTokenKey); err != nil {
		return b, err
	}
	users, err := s.Setting(ctx, tgUsersKey)
	if err != nil {
		return b, err
	}
	b.Users, _ = ParseTGUsers(users) // в базе только проверенное; испорченное — как пусто
	if b.Page, err = s.Setting(ctx, tgPageKey); err != nil {
		return b, err
	}
	if b.Page == "" {
		b.Page = TGDefaultPage
	}
	return b, nil
}

// SetTGToken — токен бота; "" — убрать (бот замолкает)
func (s *Store) SetTGToken(ctx context.Context, who, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return s.SetSetting(ctx, who, tgTokenKey, "", "Telegram: токен бота убран")
	}
	if !tokenRe.MatchString(token) {
		return ErrTGToken
	}
	return s.SetSetting(ctx, who, tgTokenKey, token, "Telegram: токен бота задан")
}

// SetTGUsers — кому бот отвечает и кому ставит «Табло»
func (s *Store) SetTGUsers(ctx context.Context, who string, users []int64) error {
	parts := make([]string, len(users))
	for i, u := range users {
		parts[i] = strconv.FormatInt(u, 10)
	}
	return s.SetSetting(ctx, who, tgUsersKey, strings.Join(parts, ","),
		fmt.Sprintf("Telegram: получателей %d", len(users)))
}

// SetTGPage — адрес табло; "" — по умолчанию
func (s *Store) SetTGPage(ctx context.Context, who, page string) error {
	page = strings.TrimSpace(page)
	if page != "" && !strings.HasPrefix(page, "https://") {
		return ErrTGPage
	}
	return s.SetSetting(ctx, who, tgPageKey, page, "Telegram: адрес табло")
}

// ParseTGUsers — «111222333, 123» → [111222333 123]; повторы убираются
func ParseTGUsers(text string) ([]int64, error) {
	var out []int64
	for _, f := range strings.FieldsFunc(text, func(c rune) bool { return c == ',' || c == ';' || c == ' ' }) {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil || n <= 0 {
			return nil, ErrTGUsers
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}
