//go:build !windows

package main

import "fmt"

// readSecret — вне Windows проба не нужна; ввод как есть
func readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	return readLine()
}
