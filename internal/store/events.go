package store

import (
	"context"
	"database/sql"
	"time"
)

// Event — одна авария (или предупреждение) от прихода до ухода
type Event struct {
	ID      int64
	PLCID   int64
	Signal  string
	Kind    string // alarm / warning
	Message string
	Came    time.Time
	Went    time.Time // нулевое время — авария ещё активна
}

// Active — авария ещё не ушла
func (e Event) Active() bool { return e.Went.IsZero() }

// EventEdge — фронт для записи в базу: On — авария пришла, иначе — ушла
type EventEdge struct {
	Signal  string
	Kind    string
	Message string
	On      bool
	At      time.Time
}

// SaveEdges записывает фронты одной транзакцией: при «лавине» аварий
// десятки фронтов приходят в одной выборке, и писать их по одному — медленно.
//
// Это НЕ изменение настроек, поэтому OnChange не вызывается
// (иначе каждая авария перестраивала бы опрос).
func (s *Store) SaveEdges(ctx context.Context, plcID int64, edges []EventEdge) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, e := range edges {
		if e.On {
			// Новая авария — только если по этому сигналу нет открытой (защита от двойной записи)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO events (plc_id, signal, kind, message, came_ms)
				SELECT ?, ?, ?, ?, ?
				WHERE NOT EXISTS (
					SELECT 1 FROM events WHERE plc_id = ? AND signal = ? AND went_ms IS NULL)`,
				plcID, e.Signal, e.Kind, e.Message, e.At.UnixMilli(), plcID, e.Signal)
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE events SET went_ms = ?
				WHERE plc_id = ? AND signal = ? AND went_ms IS NULL`,
				e.At.UnixMilli(), plcID, e.Signal)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ActiveEvents — активные аварии ПЛК, давние сверху
func (s *Store) ActiveEvents(ctx context.Context, plcID int64) ([]Event, error) {
	return s.queryEvents(ctx, `
		WHERE plc_id = ? AND went_ms IS NULL
		ORDER BY came_ms, id`, plcID)
}

// CloseEventsExcept закрывает активные аварии ПЛК по сигналам, которых нет в keep:
// сигнал выключили, удалили, переименовали или он больше не авария.
// Иначе такая авария висела бы на панели вечно. Возвращает, сколько закрыто.
func (s *Store) CloseEventsExcept(ctx context.Context, plcID int64, keep []string, at time.Time) (int, error) {
	open, err := s.ActiveEvents(ctx, plcID)
	if err != nil {
		return 0, err
	}
	keepSet := make(map[string]bool, len(keep))
	for _, n := range keep {
		keepSet[n] = true
	}
	var edges []EventEdge
	for _, e := range open {
		if !keepSet[e.Signal] {
			edges = append(edges, EventEdge{Signal: e.Signal, On: false, At: at})
		}
	}
	if len(edges) == 0 {
		return 0, nil
	}
	return len(edges), s.SaveEdges(ctx, plcID, edges)
}

// EventFilter — что показать в журнале
type EventFilter struct {
	PLCID int64     // 0 — все ПЛК
	From  time.Time // пришедшие с этого момента (включительно)
	To    time.Time // и до этого (не включая)
	Kind  string    // "" — аварии и предупреждения
	Limit int       // 0 — 1000
}

// Events — журнал: аварии, ПРИШЕДШИЕ в [From, To), новые сверху
func (s *Store) Events(ctx context.Context, f EventFilter) ([]Event, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	return s.queryEvents(ctx, `
		WHERE (? = 0 OR plc_id = ?)
		  AND came_ms >= ? AND came_ms < ?
		  AND (? = '' OR kind = ?)
		ORDER BY came_ms DESC, id DESC
		LIMIT ?`,
		f.PLCID, f.PLCID, f.From.UnixMilli(), f.To.UnixMilli(), f.Kind, f.Kind, f.Limit)
}

// queryEvents — общий SELECT; where — условие, сортировка и лимит
func (s *Store) queryEvents(ctx context.Context, where string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, plc_id, signal, kind, message, came_ms, went_ms FROM events `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var came int64
		var went sql.NullInt64 // NULL — авария активна
		if err := rows.Scan(&e.ID, &e.PLCID, &e.Signal, &e.Kind, &e.Message, &came, &went); err != nil {
			return nil, err
		}
		e.Came = time.UnixMilli(came)
		if went.Valid {
			e.Went = time.UnixMilli(went.Int64)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
