package store

import (
	"context"
	"strconv"
	"strings"
)

// ---------- Уведомления об авариях в Telegram (шаг 45d) ----------

// tgNotifySchema — миграция 14: галочка «уведомлять об авариях» у станка.
// По умолчанию выключена: как и всё в Telegram, сообщения уходят только после явной галочки.
const tgNotifySchema = `ALTER TABLE tg_machines ADD COLUMN notify INTEGER NOT NULL DEFAULT 0;`

// LastEventID — номер последней записи журнала аварий (0 — журнал пуст).
// Уведомления начинаются с аварий ПОСЛЕ запуска: старые при перезапуске не присылаются.
func (s *Store) LastEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id)
	return id, err
}

// EventsAfter — аварии и предупреждения, записанные после записи afterID, по порядку
func (s *Store) EventsAfter(ctx context.Context, afterID int64, limit int) ([]Event, error) {
	return s.queryEvents(ctx, `WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
}

// EventsByID — записи журнала по номерам (чтобы узнать, ушли ли объявленные аварии)
func (s *Store) EventsByID(ctx context.Context, ids []int64) ([]Event, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10) // только числа — склеивать в запрос безопасно
	}
	return s.queryEvents(ctx, `WHERE id IN (`+strings.Join(parts, ",")+`) ORDER BY id`)
}

// ---------- Закладка уведомлений: что уже сообщили (переживает перезапуск службы) ----------

const tgNotifyKey = "tg_notify_state" // «номер последней записи журнала;объявленные активные аварии через запятую»

// TGNotifyState — закладка уведомлений; ok = false — её ещё нет (первый запуск с уведомлениями)
func (s *Store) TGNotifyState(ctx context.Context) (last int64, active []int64, ok bool, err error) {
	v, err := s.Setting(ctx, tgNotifyKey)
	if err != nil || v == "" {
		return 0, nil, false, err
	}
	head, list, _ := strings.Cut(v, ";")
	if last, err = strconv.ParseInt(head, 10, 64); err != nil {
		return 0, nil, false, nil // испорчена — как будто нет
	}
	for _, f := range strings.Split(list, ",") {
		if id, err := strconv.ParseInt(f, 10, 64); err == nil {
			active = append(active, id)
		}
	}
	return last, active, true, nil
}

// SaveTGNotifyState — сохранить закладку. Это не настройка, а служебная отметка:
// в журнал изменений не пишется (иначе каждая авария давала бы строку в журнале).
func (s *Store) SaveTGNotifyState(ctx context.Context, last int64, active []int64) error {
	parts := make([]string, len(active))
	for i, id := range active {
		parts[i] = strconv.FormatInt(id, 10)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		tgNotifyKey, strconv.FormatInt(last, 10)+";"+strings.Join(parts, ","))
	return err
}
