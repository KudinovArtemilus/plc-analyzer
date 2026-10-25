package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrLastPLC = errors.New("нельзя удалить единственный ПЛК — сначала добавьте другой")

// DeletePLC удаляет ПЛК со всем, что на него ссылается в базе: сигналы, журнал аварий,
// шунты, слепки. Всё одной транзакцией — либо удалено всё, либо ничего.
// Файлы записей на диске НЕ трогает: стирать гигабайты данных молча нельзя.
// Возвращает, сколько сигналов было удалено (для сообщения).
func (s *Store) DeletePLC(ctx context.Context, who string, p PLC) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plcs`).Scan(&total); err != nil {
		return 0, err
	}
	if total <= 1 {
		return 0, ErrLastPLC
	}

	var signals int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM signals WHERE plc_id = ?`, p.ID).Scan(&signals); err != nil {
		return 0, err
	}

	// Сначала то, что ссылается на ПЛК (REFERENCES plcs), потом сам ПЛК —
	// иначе база с включёнными внешними ключами не даст его удалить
	for _, table := range []string{"signals", "events", "shelves", "snapshots"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE plc_id = ?`, p.ID); err != nil {
			return 0, fmt.Errorf("%s: %w", table, err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM plcs WHERE id = ?`, p.ID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "удалён ПЛК",
		fmt.Sprintf("%s (%s), сигналов: %d", p.Name, p.IP, signals)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.OnChange != nil {
		s.OnChange() // менеджер увидит, что ПЛК нет в базе, и остановит его опрос и запись
	}
	return signals, nil
}
