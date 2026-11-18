package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/summary"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgbot"
	"github.com/KudinovArtemilus/plc-analyzer/internal/tgnotify"
	"github.com/KudinovArtemilus/plc-analyzer/internal/web"
)

// ---------- plc-analyzer.exe telegram — бот Telegram (45b) ----------
//
//	plc-analyzer.exe telegram          — задать токен бота (скрытый ввод) и кому можно (Telegram ID)
//	plc-analyzer.exe telegram off      — убрать токен: бот замолкает
//	plc-analyzer.exe telegram -db D:\plc-analyzer\analyzer.db   — если база не в текущей папке
//
// Служба перезапускать не нужно: бот перечитывает настройки из базы каждые полминуты.

func telegramCmd(args []string) error {
	fs := flag.NewFlagSet("telegram", flag.ContinueOnError)
	db := fs.String("db", "analyzer.db", "файл базы настроек")
	if err := fs.Parse(args); err != nil {
		return err
	}
	off := fs.Arg(0) == "off"
	if fs.NArg() > 1 || (fs.NArg() == 1 && !off) {
		return errors.New(`использование: plc-analyzer.exe telegram [off] [-db analyzer.db]`)
	}
	if _, err := os.Stat(*db); err != nil {
		return fmt.Errorf("база %s не найдена — запустите из папки анализатора или укажите -db", *db)
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	const who = "командная строка"

	if off {
		if err := st.SetTGToken(ctx, who, ""); err != nil {
			return err
		}
		fmt.Println("Токен убран: бот замолчит в течение полуминуты. Список получателей сохранён.")
		return nil
	}

	cur, err := st.TGBotConfig(ctx)
	if err != nil {
		return err
	}
	state := "не задан"
	if cur.Token != "" {
		state = "задан"
	}
	fmt.Printf("Бот Telegram. Токен: %s. Получатели: %s\n", state, usersText(cur.Users))

	token, err := readSecret("Токен бота от @BotFather (не виден при вводе; Enter — оставить как есть): ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		if err := st.SetTGToken(ctx, who, token); err != nil {
			return err
		}
		fmt.Println("Токен сохранён.")
	}

	fmt.Print("Telegram ID получателей через запятую (Enter — оставить как есть): ")
	line, err := readLine()
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "" {
		users, err := store.ParseTGUsers(line)
		if err != nil {
			return err
		}
		if err := st.SetTGUsers(ctx, who, users); err != nil {
			return err
		}
		fmt.Println("Получатели:", usersText(users))
	}
	fmt.Println(`Готово. Служба подхватит в течение полуминуты, перезапуск не нужен.
Каждый получатель должен один раз написать боту /start — до этого Telegram не даёт боту ему писать.`)
	return nil
}

func usersText(u []int64) string {
	if len(u) == 0 {
		return "нет"
	}
	return strings.Trim(fmt.Sprint(u), "[]")
}

// alarmNotes — уведомления об авариях (45d): журнал аварий → тексты для бота
func alarmNotes(st *store.Store) func(context.Context) ([]tgbot.Note, error) {
	w := tgnotify.New(st)
	return func(ctx context.Context) ([]tgbot.Note, error) {
		notes, err := w.Notes(ctx, time.Now())
		out := make([]tgbot.Note, len(notes))
		for i, n := range notes {
			out[i] = tgbot.Note{Text: n.Text, Silent: n.Silent}
		}
		return out, err
	}
}

// startBot — бот в фоне службы; закроет done, когда остановится
func startBot(ctx context.Context, st *store.Store, srv *web.Server) (done chan struct{}) {
	bot := tgbot.New(tgbot.Deps{
		Load: func(ctx context.Context) (tgbot.Config, error) {
			c, err := st.TGBotConfig(ctx)
			if err != nil {
				return tgbot.Config{}, err
			}
			p, err := st.TGPeriod(ctx)
			return tgbot.Config{Token: c.Token, Users: c.Users, Page: c.Page, Period: p}, err
		},
		Summary: func(ctx context.Context) (summary.Summary, error) { return srv.TGSummary(ctx, time.Now()) },
		Notes:   alarmNotes(st),
		Report:  func(ctx context.Context) (string, error) { return srv.TGReport(ctx, time.Now()) },
	})
	srv.TGBot = bot.Status
	done = make(chan struct{})
	go func() {
		defer close(done)
		bot.Run(ctx)
	}()
	return done
}
