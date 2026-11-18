package web

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"
)

// Запись станка в фикстуре: 08:00–09:30 (давление 180), «сейчас» — 10:00
func TestTGAsk(t *testing.T) {
	s, _, ctx, now, _, _ := tgSummaryServer(t)

	// За 2 часа — картинка и статистика из записей
	rep, err := s.TGAsk(ctx, "давление шлифовка за 2 часа", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(rep.Photo)); err != nil {
		t.Fatalf("картинка: %v", err)
	}
	want := "📈 Шлифовка · 08:00–10:00 (2 ч)\n1 🔵 Давление, бар: мин 180 (08:00:00) · макс 180 (08:00:00) · среднее 180"
	if rep.Text != want {
		t.Errorf("подпись:\n%s\nждали:\n%s", rep.Text, want)
	}

	// Только график — картинка, в подписи без статистики
	rep, _ = s.TGAsk(ctx, "график давления за 2 часа", now)
	if rep.Photo == nil || rep.Text != "📈 Шлифовка · 08:00–10:00 (2 ч)\n1 🔵 Давление, бар" {
		t.Errorf("только график: %q", rep.Text)
	}

	// Только статистика — текстом, без картинки
	rep, _ = s.TGAsk(ctx, "статистика давления за 2 часа", now)
	if rep.Photo != nil || !strings.Contains(rep.Text, "Давление, бар: мин 180") || strings.Contains(rep.Text, "🔵") {
		t.Errorf("статистика: %+v", rep.Text)
	}

	// Последние 10 минут — записей нет (запись кончилась в 09:30)
	rep, _ = s.TGAsk(ctx, "давление 10 мин", now)
	if rep.Photo != nil || !strings.Contains(rep.Text, "записей нет") {
		t.Errorf("пустой участок: %q", rep.Text)
	}

	// Сигнала нет на карточке — подсказка, какие есть; «Температура» на карточку не добавлена
	rep, _ = s.TGAsk(ctx, "температура шлифовка", now)
	if !strings.Contains(rep.Text, "Не понял, какой сигнал") || !strings.Contains(rep.Text, "Давление, бар, Шаг") {
		t.Errorf("неизвестный сигнал: %q", rep.Text)
	}

	// «Пресс» выключен — о нём спросить нельзя (единственный разрешённый станок — Шлифовка)
	if rep, _ = s.TGAsk(ctx, "помощь", now); strings.Contains(rep.Text, "Пресс") || !strings.Contains(rep.Text, "Шлифовка: Давление, бар, Шаг") {
		t.Errorf("помощь: %q", rep.Text)
	}
}

// Участок через начало смены: часть точек — из прошлой смены
func TestTGWindowAcrossShifts(t *testing.T) {
	s, _, _, now, _, _ := tgSummaryServer(t)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	d := s.tgWindow("SGGT", []string{"Давление"}, day.Add(7*time.Hour+50*time.Minute), day.Add(8*time.Hour+10*time.Minute))
	got := d["Давление"]
	if len(got.t) != 601 || got.periodMs != 1000 { // 08:00:00…08:10:00 включительно, точка в секунду
		t.Errorf("точек %d, период %d", len(got.t), got.periodMs)
	}
}
