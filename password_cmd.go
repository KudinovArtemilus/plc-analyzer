package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/web"
)

// ---------- plc-analyzer.exe password — пароль для просмотра с других ПК (37) ----------
//
//	plc-analyzer.exe password          — задать (или сменить) пароль; спросит дважды
//	plc-analyzer.exe password off      — снять пароль: смотреть снова может любой ПК
//	plc-analyzer.exe password -db D:\plc-analyzer\analyzer.db   — если база не в текущей папке
//
// Служба перезапускать не нужно: пароль читается из базы при каждом входе.

func passwordCmd(args []string) error {
	fs := flag.NewFlagSet("password", flag.ContinueOnError)
	db := fs.String("db", "analyzer.db", "файл базы настроек")
	if err := fs.Parse(args); err != nil {
		return err
	}
	off := fs.Arg(0) == "off"
	if fs.NArg() > 1 || (fs.NArg() == 1 && !off) {
		return errors.New(`использование: plc-analyzer.exe password [off] [-db analyzer.db]`)
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

	if off {
		if err := st.SetSetting(ctx, "командная строка", web.PasswordKey(), "", "пароль для просмотра снят"); err != nil {
			return err
		}
		fmt.Println("Пароль снят: смотреть анализатор снова может любой ПК, которому открыт порт.")
		return nil
	}

	pw, err := readSecret("Новый пароль для просмотра: ")
	if err != nil {
		return err
	}
	again, err := readSecret("Ещё раз: ")
	if err != nil {
		return err
	}
	if pw != again {
		return errors.New("пароли не совпали — ничего не изменено")
	}
	hash, err := web.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := st.SetSetting(ctx, "командная строка", web.PasswordKey(), hash, "пароль для просмотра задан"); err != nil {
		return err
	}
	fmt.Println("Пароль задан. С других ПК теперь нужен вход; на этом ПК — без пароля.")
	return nil
}

// stdin — ОДИН читатель на всю программу. bufio читает с запасом: новый bufio.Reader на каждый
// вопрос «съел» бы и ответ на следующий (так и было на пробе: второй ответ пропадал).
var stdin = bufio.NewReader(os.Stdin)

// readLine — строка с клавиатуры (общая часть для readSecret)
func readLine() (string, error) {
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
