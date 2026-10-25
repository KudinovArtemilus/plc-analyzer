package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AddSignals добавляет пачку сигналов одной транзакцией: либо все, либо ни одного.
// Опрос перестраивается ОДИН раз (а не 64 раза при добавлении 64 битов).
// В журнал изменений — одна запись со списком имён.
func (s *Store) AddSignals(ctx context.Context, who string, rows []SignalRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO signals (plc_id, name, addr, type, period_ms, scale, bias, unit, enabled, kind, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	names := make([]string, len(rows))
	for i, r := range rows {
		r = normalize(r)
		if _, err := stmt.ExecContext(ctx, r.PLCID, r.Name, r.Addr, r.Type, r.PeriodMs,
			r.Scale, r.Bias, r.Unit, r.Enabled, r.Kind, r.Message); err != nil {
			// Имя, на котором споткнулись, — чтобы было понятно, какая строка виновата
			return fmt.Errorf("сигнал %q: %w", r.Name, err)
		}
		names[i] = r.Name
	}

	details := fmt.Sprintf("%d шт.: %s", len(rows), strings.Join(names, ", "))
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "добавлены сигналы списком", details); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return nil
}
