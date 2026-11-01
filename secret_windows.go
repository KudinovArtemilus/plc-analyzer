package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// readSecret — ввод пароля без отображения на экране: у консоли Windows на время ввода
// выключается «эхо» (ENABLE_ECHO_INPUT), потом режим возвращается как был.
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err == nil {
		windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT)
		defer windows.SetConsoleMode(h, mode)
		defer fmt.Println() // Enter не отобразился — перевести строку самим
	}
	return readLine()
}
