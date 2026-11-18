package store

import "context"

// ---------- Итог смены в Telegram (шаг 45e) ----------
//
// Две настройки в settings, без миграции:
//   tg_report       — "1": присылать итог смены в 08:00 и 20:00 (по умолчанию — нет);
//   tg_report_last  — за какую смену итог уже отправлен («2026-11-18 A»); "now" — прислать
//                     итог прошлой смены сейчас (кнопка на странице, для проверки).

const (
	tgReportKey     = "tg_report"
	tgReportLastKey = "tg_report_last"

	// TGReportNow — значение tg_report_last: «прислать итог прошлой смены сейчас»
	TGReportNow = "now"
)

// TGReport — включён ли итог смены и за какую смену он уже ушёл
func (s *Store) TGReport(ctx context.Context) (on bool, last string, err error) {
	v, err := s.Setting(ctx, tgReportKey)
	if err != nil {
		return false, "", err
	}
	last, err = s.Setting(ctx, tgReportLastKey)
	return v == "1", last, err
}

// SetTGReport — включить / выключить итог смены
func (s *Store) SetTGReport(ctx context.Context, who string, on bool) error {
	if on {
		return s.SetSetting(ctx, who, tgReportKey, "1", "Telegram: итог смены включён")
	}
	return s.SetSetting(ctx, who, tgReportKey, "", "Telegram: итог смены выключен")
}

// RequestTGReport — кнопка «прислать итог прошлой смены сейчас»
func (s *Store) RequestTGReport(ctx context.Context, who string) error {
	return s.SetSetting(ctx, who, tgReportLastKey, TGReportNow, "Telegram: итог смены — прислать сейчас")
}

// SetTGReportLast — отметка «итог за эту смену отправлен». Служебная: в журнал изменений не пишется.
func (s *Store) SetTGReportLast(ctx context.Context, shift string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, tgReportLastKey, shift)
	return err
}
