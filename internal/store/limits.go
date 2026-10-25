package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Limit — нормы аналогового сигнала: границы и скачок.
// Отдельная таблица, а не новые столбцы в signals: у большинства сигналов норм нет,
// и формы сигналов, массовое добавление и запросы к signals остаются как были.
type Limit struct {
	SignalID int64
	Lo, Hi   *float64 // нижняя и верхняя граница; nil — границы нет
	Hyst     float64  // зона возврата: норма восстановилась, только когда значение вернулось на Hyst внутрь
	Jump     *float64 // скачок: изменение больше Jump за JumpMs; nil — не следим
	JumpMs   int64
	Kind     string // во что превращается нарушение: warning или alarm
}

// Empty — норм нет ни одной (такую запись хранить незачем)
func (l Limit) Empty() bool { return l.Lo == nil && l.Hi == nil && l.Jump == nil }

// Check — правила для норм; те же для формы и для базы
func (l Limit) Check() error {
	switch {
	case l.Kind != KindWarning && l.Kind != KindAlarm:
		return errors.New("нарушение нормы — предупреждение или авария")
	case l.Lo != nil && l.Hi != nil && *l.Lo >= *l.Hi:
		return errors.New("нижняя граница должна быть меньше верхней")
	case l.Hyst < 0:
		return errors.New("зона возврата не может быть отрицательной")
	case l.Lo != nil && l.Hi != nil && 2*l.Hyst >= *l.Hi-*l.Lo:
		return errors.New("зона возврата слишком большая: больше половины диапазона нормы")
	case l.Jump != nil && *l.Jump <= 0:
		return errors.New("скачок — положительное число")
	case l.Jump != nil && (l.JumpMs < 10 || l.JumpMs > 600_000):
		return errors.New("время скачка — от 10 мс до 10 минут")
	}
	return nil
}

// limitsSchema — миграция 4 (добавляется в конец списка migrations в store.go).
// ON DELETE CASCADE: удалили сигнал — его нормы удаляются сами.
const limitsSchema = `CREATE TABLE limits (
	signal_id INTEGER PRIMARY KEY REFERENCES signals(id) ON DELETE CASCADE,
	lo        REAL,
	hi        REAL,
	hyst      REAL    NOT NULL DEFAULT 0,
	jump      REAL,
	jump_ms   INTEGER NOT NULL DEFAULT 0,
	kind      TEXT    NOT NULL DEFAULT 'warning'
 );`

// Limits — нормы всех сигналов ПЛК: номер сигнала → нормы
func (s *Store) Limits(ctx context.Context, plcID int64) (map[int64]Limit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.signal_id, l.lo, l.hi, l.hyst, l.jump, l.jump_ms, l.kind
		FROM limits l JOIN signals s ON s.id = l.signal_id
		WHERE s.plc_id = ?`, plcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]Limit{}
	for rows.Next() {
		var l Limit
		// *float64 в Scan: NULL в базе → nil, число → указатель на него
		if err := rows.Scan(&l.SignalID, &l.Lo, &l.Hi, &l.Hyst, &l.Jump, &l.JumpMs, &l.Kind); err != nil {
			return nil, err
		}
		out[l.SignalID] = l
	}
	return out, rows.Err()
}

// SetLimit — записать нормы сигнала (новые или поверх старых). Пустые нормы — удалить.
func (s *Store) SetLimit(ctx context.Context, who string, sig SignalRow, l Limit) error {
	l.SignalID = sig.ID
	if l.Empty() {
		return s.DeleteLimit(ctx, who, sig)
	}
	if err := l.Check(); err != nil {
		return err
	}
	// UPSERT: строки нет — вставить, есть — обновить. Одним запросом, без «сначала проверить».
	_, err := s.change(ctx, who, "нормы сигнала", sig.Name+": "+DescribeLimit(l, sig.Unit), `
		INSERT INTO limits (signal_id, lo, hi, hyst, jump, jump_ms, kind) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (signal_id) DO UPDATE SET
			lo = excluded.lo, hi = excluded.hi, hyst = excluded.hyst,
			jump = excluded.jump, jump_ms = excluded.jump_ms, kind = excluded.kind`,
		l.SignalID, l.Lo, l.Hi, l.Hyst, l.Jump, l.JumpMs, l.Kind)
	return err
}

// DeleteLimit — убрать все нормы сигнала
func (s *Store) DeleteLimit(ctx context.Context, who string, sig SignalRow) error {
	_, err := s.change(ctx, who, "нормы сигнала", sig.Name+": сняты",
		`DELETE FROM limits WHERE signal_id = ?`, sig.ID)
	if errors.Is(err, ErrNotFound) {
		return nil // норм и не было — снимать нечего, это не ошибка
	}
	return err
}

// DescribeLimit — нормы одной строкой: «140…180 бар, возврат 2 · скачок > 20 за 1000 мс · авария»
func DescribeLimit(l Limit, unit string) string {
	if l.Empty() {
		return "нет"
	}
	num := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	u := ""
	if unit != "" {
		u = " " + unit
	}
	d := ""
	switch {
	case l.Lo != nil && l.Hi != nil:
		d = num(*l.Lo) + "…" + num(*l.Hi) + u
	case l.Lo != nil:
		d = "не ниже " + num(*l.Lo) + u
	case l.Hi != nil:
		d = "не выше " + num(*l.Hi) + u
	}
	if d != "" && l.Hyst > 0 {
		d += ", возврат " + num(l.Hyst)
	}
	if l.Jump != nil {
		if d != "" {
			d += " · "
		}
		d += fmt.Sprintf("скачок > %s%s за %d мс", num(*l.Jump), u, l.JumpMs)
	}
	kind := "предупреждение"
	if l.Kind == KindAlarm {
		kind = "авария"
	}
	return d + " · " + kind
}
