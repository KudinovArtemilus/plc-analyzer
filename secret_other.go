//go:build !windows

package main

import "fmt"

// readSecret — на других системах (только для разработки) пароль виден при вводе
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	return readLine()
}
