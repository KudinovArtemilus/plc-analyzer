package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---------- Настройки программы: «ключ — значение» (37) ----------
//
// Пока здесь одна настройка — хеш пароля для просмотра с других ПК. Таблица общая:
// следующая настройка — новая строка, а не новая миграция.

// settingsSchema — миграция 10
const settingsSchema = `CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
 );`

// Setting — значение настройки; "" — не задана
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting — задать настройку; value "" — удалить. В журнал изменений пишется action,
// а не само значение: в журнале не должно быть даже хеша пароля.
func (s *Store) SetSetting(ctx context.Context, who, key, value, action string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if value == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, action, key); err != nil {
		return err
	}
	return tx.Commit()
}
