// tgprobe — проба перед шагом 45b: можно ли передать сводку в Mini App прямо в ссылке кнопки.
//
// Бот (ваш, из BotFather) присылает вам кнопки «Табло» со ссылкой на пробную страницу
// docs/tg/probe.html (GitHub Pages), к ссылке приписаны данные разной длины — после # и после ?.
// Открываете кнопки на телефоне — страница показывает, дошли ли данные целиком.
//
// Запуск (из папки проекта):
//
//	go run ./tools/tgprobe -url https://<логин>.github.io/plc-analyzer/tg/probe.html
//
// Токен вводится скрытно и никуда не пишется: ни в файл, ни в лог, ни в вывод
// (в ошибках он заменяется на ***). Программа ничего не знает об анализаторе и ПЛК.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var stdin = bufio.NewReader(os.Stdin)

func readLine() (string, error) {
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// bot — вызовы Bot API. Токен живёт только в памяти.
type bot struct {
	token string
	http  *http.Client
}

// call — метод Bot API с JSON-параметрами; ответ result → out
func (b bot) call(method string, params any, out any) error {
	body, _ := json.Marshal(params)
	resp, err := b.http.Post("https://api.telegram.org/bot"+b.token+"/"+method, "application/json", bytes.NewReader(body))
	if err != nil {
		// В тексте ошибки net/http есть адрес — а в адресе токен. Прячем.
		return errors.New(strings.ReplaceAll(err.Error(), b.token, "***"))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("%s: ответ не JSON (HTTP %d)", method, resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("%s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// payload — данные ровно n символов: «P<n>_» и дальше буквы base64url.
// Страница проверяет: начало на месте и длина та же — значит, дошло целиком.
func payload(n int) string {
	const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var sb strings.Builder
	sb.WriteString("P" + strconv.Itoa(n) + "_")
	for i := 0; sb.Len() < n; i++ {
		sb.WriteByte(abc[(i*7+3)%len(abc)])
	}
	return sb.String()[:n]
}

func main() {
	page := flag.String("url", "", "адрес пробной страницы на GitHub Pages (…/tg/probe.html)")
	flag.Parse()
	if !strings.HasPrefix(*page, "https://") {
		fmt.Println("Нужен адрес страницы: go run ./tools/tgprobe -url https://<логин>.github.io/plc-analyzer/tg/probe.html")
		os.Exit(2)
	}
	token, err := readSecret("Токен бота из BotFather (при вводе не виден): ")
	if err != nil || strings.TrimSpace(token) == "" {
		fmt.Println("Токен не введён")
		os.Exit(2)
	}
	b := bot{token: strings.TrimSpace(token), http: &http.Client{Timeout: 60 * time.Second}}

	var me struct{ Username string }
	if err := b.call("getMe", nil, &me); err != nil {
		fmt.Println("Бот не отвечает:", err)
		fmt.Println("Проверьте токен и что на этом ПК открывается https://api.telegram.org")
		os.Exit(1)
	}
	fmt.Printf("Бот @%s на связи.\nОткройте его в Telegram и отправьте /start — жду до 5 минут…\n", me.Username)

	chat, user := waitStart(b)
	if chat == 0 {
		fmt.Println("Сообщение не пришло. Запустите ещё раз.")
		os.Exit(1)
	}
	fmt.Printf("Пришло. Ваш Telegram ID: %d — запишите, он понадобится для списка «кому можно».\n\n", user)

	// Кнопки: данные после # и после ?, длины от 500 до 8000 символов
	lengths := []int{500, 1000, 2000, 3000, 4000, 6000, 8000}
	best := ""
	fmt.Println("Место  Длина  Telegram принял?")
	for _, place := range []string{"#", "?"} {
		for _, n := range lengths {
			link := *page + place + "d=" + payload(n)
			text := fmt.Sprintf("Проба: данные после «%s», %d символов (вся ссылка — %d)", place, n, len(link))
			err := b.call("sendMessage", map[string]any{
				"chat_id": chat, "text": text,
				"reply_markup": map[string]any{"inline_keyboard": [][]map[string]any{{
					{"text": "Открыть " + place + " " + strconv.Itoa(n), "web_app": map[string]string{"url": link}},
				}}},
			}, nil)
			res := "да"
			if err != nil {
				res = "НЕТ — " + err.Error()
			} else if place == "#" {
				best = link
			}
			fmt.Printf("  %s    %5d  %s\n", place, n, res)
			time.Sleep(300 * time.Millisecond) // не чаще ~3 сообщений в секунду
		}
	}

	// Кнопка меню рядом с полем ввода — так будет выглядеть «Табло» в 45b
	if best != "" {
		err := b.call("setChatMenuButton", map[string]any{"chat_id": chat,
			"menu_button": map[string]any{"type": "web_app", "text": "Табло", "web_app": map[string]string{"url": best}}}, nil)
		if err != nil {
			fmt.Println("\nКнопка меню «Табло»: НЕТ —", err)
		} else {
			fmt.Println("\nКнопка меню «Табло» (слева от поля ввода) поставлена с самыми длинными данными после #.")
		}
	}
	fmt.Println("\nТеперь откройте кнопки на телефоне (и на ПК, если есть Telegram Desktop):")
	fmt.Println("страница покажет «дошло целиком» или «обрезано». Пришлите мне эту таблицу и что показала страница.")
}

// waitStart — ждём первое сообщение боту (long polling: бот сам спрашивает Telegram, вход не нужен)
func waitStart(b bot) (chat, user int64) {
	deadline := time.Now().Add(5 * time.Minute)
	offset := 0
	for time.Now().Before(deadline) {
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
			} `json:"message"`
		}
		if err := b.call("getUpdates", map[string]any{"offset": offset, "timeout": 30}, &ups); err != nil {
			fmt.Println("Ошибка:", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			if u.Message != nil && u.Message.Chat.Type == "private" {
				b.call("getUpdates", map[string]any{"offset": offset, "timeout": 0}, nil) // отметить прочитанным
				return u.Message.Chat.ID, u.Message.From.ID
			}
		}
	}
	return 0, 0
}
