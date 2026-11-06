package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/KudinovArtemilus/plc-analyzer/internal/about"
)

// Свойства exe (winres/winres.json → rsrc_windows_*.syso) пишутся отдельно от пакета about:
// JSON не умеет брать строки из Go. Тест следит, чтобы автор и год там не разошлись.
// Поменяли winres.json — пересоздайте .syso: go-winres make
func TestWinresAbout(t *testing.T) {
	b, err := os.ReadFile("winres/winres.json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Version map[string]map[string]struct {
			Info map[string]map[string]string `json:"info"`
		} `json:"RT_VERSION"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	info := res.Version["#1"]["0000"].Info["0419"]
	want := map[string]string{
		"ProductName":    about.Name,
		"CompanyName":    about.Author,
		"LegalCopyright": fmt.Sprintf("© %d %s", about.Year, about.Author),
	}
	for k, v := range want {
		if info[k] != v {
			t.Errorf("winres.json: %s = %q, а в пакете about — %q", k, info[k], v)
		}
	}
	// .syso — уже собранные ресурсы: их берёт go build. Время файлов не сравниваем —
	// после git clone оно у всех файлов одинаковое и ничего не говорит
	for _, f := range []string{"rsrc_windows_amd64.syso", "rsrc_windows_386.syso"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("нет %s — выполните go-winres make", f)
		}
	}
}
