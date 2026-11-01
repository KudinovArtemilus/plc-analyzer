package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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
	Marks    []Mark // отметки «вот здесь проблема»: приход аварии или поставленные вручную
}

// Mark — отметка на слепке: момент и подпись
type Mark struct {
	Ms   int64  `json:"ms"`
	Text string `json:"text"`
}

// marksSchema — миграция 9: отметки на слепках, по одной на строку «мс<TAB>текст».
// Отдельная таблица была бы «правильнее», но отметок на слепке единицы, читаются они только
// вместе со слепком — строки в одном поле хватает (так же хранятся и сигналы слепка).
const marksSchema = `ALTER TABLE snapshots ADD COLUMN marks TEXT NOT NULL DEFAULT '';`

func formatMarks(ms []Mark) string {
	lines := make([]string, len(ms))
	for i, m := range ms {
		// табуляция и перевод строки — разделители, в подписи их быть не должно
		text := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(m.Text)
		lines[i] = strconv.FormatInt(m.Ms, 10) + "\t" + text
	}
	return strings.Join(lines, "\n")
}

func parseMarks(s string) []Mark {
	var out []Mark
	for _, line := range strings.Split(s, "\n") {
		ms, text, _ := strings.Cut(line, "\t")
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil {
			out = append(out, Mark{Ms: n, Text: text})
		}
	}
	return out
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
		INSERT INTO snapshots (plc_id, name, category, comment, file, from_ms, to_ms, signals, created, who, marks)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sn.PLCID, sn.Name, sn.Category, sn.Comment, sn.File,
		sn.FromMs, sn.ToMs, strings.Join(sn.Signals, "\n"), now, who, formatMarks(sn.Marks))
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
		SELECT `+snapCols+` FROM snapshots
		WHERE plc_id = ? AND (? = '' OR category = ?)
		ORDER BY id DESC`, plcID, category, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

const snapCols = `id, plc_id, name, category, comment, file, from_ms, to_ms, signals, created, who, marks`

func scanSnapshot(sc interface{ Scan(...any) error }) (Snapshot, error) {
	var sn Snapshot
	var sigs, created, marks string
	err := sc.Scan(&sn.ID, &sn.PLCID, &sn.Name, &sn.Category, &sn.Comment, &sn.File,
		&sn.FromMs, &sn.ToMs, &sigs, &created, &sn.Who, &marks)
	sn.Signals = strings.Split(sigs, "\n")
	sn.Created, _ = time.Parse(time.RFC3339, created)
	sn.Marks = parseMarks(marks)
	return sn, err
}

// SnapshotByFile — слепок ПЛК по его файлу (snapshots/A_….pla); ErrNotFound — такого нет
func (s *Store) SnapshotByFile(ctx context.Context, plcID int64, file string) (Snapshot, error) {
	sn, err := scanSnapshot(s.db.QueryRowContext(ctx,
		`SELECT `+snapCols+` FROM snapshots WHERE plc_id = ? AND file = ?`, plcID, file))
	if errors.Is(err, sql.ErrNoRows) {
		return sn, ErrNotFound
	}
	return sn, err
}

// AddMark — добавить отметку на слепок и запись в журнал изменений.
// Прочитать–дописать–записать в одной транзакции: две отметки одновременно не потеряют друг друга.
func (s *Store) AddMark(ctx context.Context, who string, plcID int64, file string, m Mark) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	var marks, name string
	err = tx.QueryRowContext(ctx, `SELECT id, marks, name FROM snapshots WHERE plc_id = ? AND file = ?`,
		plcID, file).Scan(&id, &marks, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	list := append(parseMarks(marks), m)
	if _, err := tx.ExecContext(ctx, `UPDATE snapshots SET marks = ? WHERE id = ?`, formatMarks(list), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, "отметка на слепке",
		fmt.Sprintf("%s: %s %s", name, time.UnixMilli(m.Ms).Format("15:04:05.000"), m.Text)); err != nil {
		return err
	}
	return tx.Commit()
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
