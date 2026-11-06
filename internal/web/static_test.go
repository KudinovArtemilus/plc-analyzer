package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Если вставить новый файл в открытую вкладку без Ctrl+A, старый текст останется, и всё
// объявится дважды. В JS второй const с тем же именем — ошибка разбора: не работает весь файл
// (isa101.js — нет цветов ISA, нет графиков). В CSS дубль ничего не ломает, но правила
// перебивают друг друга. Тест ловит оба случая до того, как exe уедет на завод.
func TestStaticNoDuplicates(t *testing.T) {
	jsDecl := regexp.MustCompile(`(?m)^(?:const|let|var|function|class)\s+(\w+)`)
	jsFiles, _ := fs.Glob(static, "static/*.js")
	for _, f := range jsFiles {
		if strings.HasSuffix(f, ".min.js") || strings.HasSuffix(f, ".iife.min.js") {
			continue // чужие библиотеки (htmx, uPlot)
		}
		b, _ := static.ReadFile(f)
		seen := map[string]bool{}
		for _, m := range jsDecl.FindAllStringSubmatch(string(b), -1) {
			if seen[m[1]] {
				t.Errorf("%s: %s объявлен дважды — файл вставлен поверх старого?", f, m[1])
			}
			seen[m[1]] = true
		}
	}

	// CSS: одинаковый комментарий-заголовок раздела дважды — тот же признак
	cssHead := regexp.MustCompile(`(?m)^/\* .+ \*/$`)
	cssFiles, _ := fs.Glob(static, "static/*.css")
	for _, f := range cssFiles {
		b, _ := static.ReadFile(f)
		seen := map[string]bool{}
		for _, h := range cssHead.FindAllString(string(b), -1) {
			if seen[h] {
				t.Errorf("%s: раздел %s дважды — файл вставлен поверх старого?", f, h)
				break
			}
			seen[h] = true
		}
	}
}
