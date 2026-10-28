package main

import (
	"errors"
	"flag"
	"path/filepath"
	"testing"
)

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"-addr", ":8080", "-data", `E:\rec`})
	if err != nil {
		t.Fatal(err)
	}
	if o.addr != ":8080" || o.db != "analyzer.db" {
		t.Errorf("addr %q, db %q", o.addr, o.db)
	}
	// Лог по умолчанию — в папке -data, даже если -data задан после -log
	if want := filepath.Join(`E:\rec`, "plc-analyzer.log"); o.log != want {
		t.Errorf("лог %q, ждали %q", o.log, want)
	}

	if o, _ := parseFlags([]string{"-log", "-"}); o.log != "-" {
		t.Errorf(`-log - должен значить «только консоль», а получилось %q`, o.log)
	}

	// Опечатки — ошибка, а не тихий запуск с настройками по умолчанию
	for _, bad := range [][]string{
		{"-adr", ":8080"},     // нет такого флага
		{"addr", ":8080"},     // без дефиса — лишние слова
		{"-keep", "тридцать"}, // не число
	} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("%v: ждали ошибку", bad)
		}
	}

	if _, err := parseFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: ждали flag.ErrHelp, получили %v", err)
	}
}

func TestIsCommand(t *testing.T) {
	for _, c := range []string{"install", "uninstall", "start", "stop", "status"} {
		if !isCommand(c) {
			t.Errorf("%s — команда", c)
		}
	}
	for _, c := range []string{"-addr", "Install", "run", ""} {
		if isCommand(c) {
			t.Errorf("%q — не команда", c)
		}
	}
}
