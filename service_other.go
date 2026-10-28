//go:build !windows

package main

// Заглушки для других систем (Linux, macOS): там нет служб Windows.
// Нужны, чтобы проект собирался и тесты шли где угодно, например в CI на GitHub.
// Строка //go:build !windows выше — условие сборки: «только НЕ для Windows».

import "errors"

func isService() bool { return false }

func runService(options) error { return errors.New("служба Windows: только в Windows") }

func command(string, []string) error {
	return errors.New("управление службой — только в Windows")
}
