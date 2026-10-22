package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const snapshotSchema = `
CREATE TABLE IF NOT EXISTS snapshots (
	id       INTEGER PRIMARY KEY,
	plc_id   INTEGER NOT NULL REFERENCES plcs(id),
	name     TEXT    NOT NULL,
	category TEXT    NOT NULL DEFAULT '',
	comment  TEXT    NOT NULL DEFAULT '',
	file     TEXT    NOT NULL,  -- путь внутри папки ПЛК: snapshots/S_….pla
	from_ms  INTEGER NOT NULL,
	to_ms    INTEGER NOT NULL,
	signals  TEXT    NOT NULL,  -- имена сигналов, по одному на строку
	created  TEXT    NOT NULL,
	who      TEXT    NOT NULL
);
`

type Snapshot struct {
	ID       int64
	PLCID    int64
	Name     string
	Category string
	Comment  string
	File     string
	FromMs   int64
	ToMs     int64
	Signals  []string
	Created  time.Time
	Who      string
}

// AddSnapshot сохраняет описание слепка и запись в журнал.
// Это НЕ изменение настроек опроса, поэтому OnChange не вызывается:
// иначе каждый слепок перестраивал бы опрос и начинал новый файл записи.
func (s *Store) AddSnapshot(ctx context.Context, who string, sn Snapshot) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `
		INSERT INTO snapshots (plc_id, name, category, comment, file, from_ms, to_ms, signals, created, who)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sn.PLCID, sn.Name, sn.Category, sn.Comment, sn.File,
		sn.FromMs, sn.ToMs, strings.Join(sn.Signals, "\n"), now, who)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		now, who, "создан слепок", fmt.Sprintf("%s [%s]", sn.Name, sn.Category)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Snapshots — слепки ПЛК, новые сверху; category "" — все
func (s *Store) Snapshots(ctx context.Context, plcID int64, category string) ([]Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, plc_id, name, category, comment, file, from_ms, to_ms, signals, created, who
		FROM snapshots
		WHERE plc_id = ? AND (? = '' OR category = ?)
		ORDER BY id DESC`, plcID, category, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var sigs, created string
		if err := rows.Scan(&sn.ID, &sn.PLCID, &sn.Name, &sn.Category, &sn.Comment, &sn.File,
			&sn.FromMs, &sn.ToMs, &sigs, &created, &sn.Who); err != nil {
			return nil, err
		}
		sn.Signals = strings.Split(sigs, "\n")
		sn.Created, _ = time.Parse(time.RFC3339, created)
		out = append(out, sn)
	}
	return out, rows.Err()
}

// Categories — все категории слепков ПЛК, для подсказок и фильтра
func (s *Store) Categories(ctx context.Context, plcID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT category FROM snapshots
		WHERE plc_id = ? AND category <> '' ORDER BY category`, plcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{} // пустой, а не nil — в JSON будет [], а не null
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
