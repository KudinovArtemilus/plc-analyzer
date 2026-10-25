package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Shelf — шунт: аварию временно убрали с панели (ISA-18.2: shelving).
// Авария при этом продолжает записываться в журнал событий — шунт прячет её от глаз,
// а не отключает контроль. По истечении срока авария сама возвращается на панель.
type Shelf struct {
	ID        int64
	PLCID     int64
	Signal    string
	Reason    string
	Who       string
	From      time.Time
	Until     time.Time
	Removed   time.Time // нулевое — не снимали вручную
	RemovedBy string
}

// ActiveAt — действует ли шунт в момент t
func (sh Shelf) ActiveAt(t time.Time) bool {
	return sh.Removed.IsZero() && t.Before(sh.Until)
}

// MaxShelf — самый долгий шунт. Бессрочного нет намеренно: «забытый» шунт —
// это авария, которую никто не видит месяцами.
const MaxShelf = 7 * 24 * time.Hour

var ErrShelved = errors.New("эта авария уже зашунтирована")

// AddShelf ставит шунт и записывает это в журнал изменений.
// OnChange не вызывается: шунт не меняет опрос.
func (s *Store) AddShelf(ctx context.Context, sh Shelf) (int64, error) {
	switch {
	case sh.Reason == "":
		return 0, errors.New("укажите причину шунтирования")
	case !sh.Until.After(sh.From):
		return 0, errors.New("срок шунта должен быть в будущем")
	case sh.Until.Sub(sh.From) > MaxShelf:
		return 0, fmt.Errorf("шунт не дольше %v", MaxShelf)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Второй шунт на ту же аварию не ставим — иначе непонятно, какой срок главный
	var n int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM shelves
		WHERE plc_id = ? AND signal = ? AND removed_ms IS NULL AND until_ms > ?`,
		sh.PLCID, sh.Signal, sh.From.UnixMilli()).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, ErrShelved
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO shelves (plc_id, signal, reason, who, from_ms, until_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sh.PLCID, sh.Signal, sh.Reason, sh.Who, sh.From.UnixMilli(), sh.Until.UnixMilli())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), sh.Who, "авария зашунтирована",
		fmt.Sprintf("%s до %s — %s", sh.Signal, sh.Until.Format("02.01 15:04"), sh.Reason)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// RemoveShelf снимает шунт раньше срока (авария сразу вернётся на панель)
func (s *Store) RemoveShelf(ctx context.Context, who string, id int64, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var signal string
	err = tx.QueryRowContext(ctx, `
		SELECT signal FROM shelves WHERE id = ? AND removed_ms IS NULL AND until_ms > ?`,
		id, at.UnixMilli()).Scan(&signal)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("шунт уже снят или истёк")
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE shelves SET removed_ms = ?, removed_by = ? WHERE id = ?`,
		at.UnixMilli(), who, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "шунт снят", signal); err != nil {
		return err
	}
	return tx.Commit()
}

// ActiveShelves — шунты ПЛК, действующие в момент at
func (s *Store) ActiveShelves(ctx context.Context, plcID int64, at time.Time) ([]Shelf, error) {
	return s.queryShelves(ctx, `
		WHERE plc_id = ? AND removed_ms IS NULL AND until_ms > ?
		ORDER BY until_ms`, plcID, at.UnixMilli())
}

// RecentShelves — все шунты ПЛК, поставленные после since (для списка с историей), новые сверху
func (s *Store) RecentShelves(ctx context.Context, plcID int64, since time.Time) ([]Shelf, error) {
	return s.queryShelves(ctx, `
		WHERE plc_id = ? AND from_ms >= ?
		ORDER BY from_ms DESC, id DESC`, plcID, since.UnixMilli())
}

func (s *Store) queryShelves(ctx context.Context, where string, args ...any) ([]Shelf, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, plc_id, signal, reason, who, from_ms, until_ms, removed_ms, removed_by
		FROM shelves `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Shelf
	for rows.Next() {
		var sh Shelf
		var from, until int64
		var removed sql.NullInt64
		if err := rows.Scan(&sh.ID, &sh.PLCID, &sh.Signal, &sh.Reason, &sh.Who,
			&from, &until, &removed, &sh.RemovedBy); err != nil {
			return nil, err
		}
		sh.From, sh.Until = time.UnixMilli(from), time.UnixMilli(until)
		if removed.Valid {
			sh.Removed = time.UnixMilli(removed.Int64)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}
