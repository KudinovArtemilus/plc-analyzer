package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// readSecret — ввод без отображения на экране (как у plc-analyzer.exe password)
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err == nil {
		windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT)
		defer windows.SetConsoleMode(h, mode)
		defer fmt.Println()
	}
	return readLine()
}
