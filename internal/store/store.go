package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	OnChange func() // вызывается после каждого успешного изменения
}

type PLC struct {
	ID   int64
	Name string
	IP   string
	Rack int
	Slot int
}

// Типы сигналов
const (
	KindTrend   = "trend"   // обычный сигнал для графиков
	KindAlarm   = "alarm"   // авария: BOOL, 1 = авария
	KindWarning = "warning" // предупреждение: BOOL, 1 = предупреждение
)

func ValidKind(k string) bool {
	return k == KindTrend || k == KindAlarm || k == KindWarning
}

type SignalRow struct {
	ID       int64
	PLCID    int64
	Name     string
	Addr     string
	Type     string
	PeriodMs int64
	Scale    float64
	Bias     float64
	Unit     string
	Enabled  bool
	Kind     string // trend / alarm / warning
	Message  string // текст для человека: «Перегрев масла гидростанции»
	Hidden   bool   // не показывать на графиках Live; опрос и запись идут как обычно
}

var ErrNotFound = errors.New("запись не найдена")

// schema — исходная схема (версия 0). Её НЕ меняем: всё новое — через migrations.
const schema = `
CREATE TABLE IF NOT EXISTS plcs (
	id    INTEGER PRIMARY KEY,
	name  TEXT    NOT NULL,
	ip    TEXT    NOT NULL,
	rack  INTEGER NOT NULL DEFAULT 0,
	slot  INTEGER NOT NULL DEFAULT 2
);

CREATE TABLE IF NOT EXISTS signals (
	id        INTEGER PRIMARY KEY,
	plc_id    INTEGER NOT NULL REFERENCES plcs(id),
	name      TEXT    NOT NULL,
	addr      TEXT    NOT NULL,
	type      TEXT    NOT NULL,
	period_ms INTEGER NOT NULL,
	scale     REAL    NOT NULL DEFAULT 1,
	bias      REAL    NOT NULL DEFAULT 0,
	unit      TEXT    NOT NULL DEFAULT '',
	enabled   INTEGER NOT NULL DEFAULT 1,
	UNIQUE (plc_id, name)
);

CREATE TABLE IF NOT EXISTS changelog (
	id      INTEGER PRIMARY KEY,
	ts      TEXT NOT NULL,
	who     TEXT NOT NULL,
	action  TEXT NOT NULL,
	details TEXT NOT NULL
);
`

// migrations — изменения схемы по порядку. Номер миграции = индекс + 1.
// Правило: уже выпущенные миграции НИКОГДА не меняем, только добавляем новые в конец.
var migrations = []string{
	// 1: тип сигнала и текст сообщения
	`ALTER TABLE signals ADD COLUMN kind    TEXT NOT NULL DEFAULT 'trend';
	 ALTER TABLE signals ADD COLUMN message TEXT NOT NULL DEFAULT '';`,

	// 2: журнал аварий и предупреждений. Одна строка = одна авария от прихода до ухода;
	//    went_ms пустое (NULL), пока авария активна.
	`CREATE TABLE events (
		id      INTEGER PRIMARY KEY,
		plc_id  INTEGER NOT NULL REFERENCES plcs(id),
		signal  TEXT    NOT NULL,
		kind    TEXT    NOT NULL,
		message TEXT    NOT NULL,
		came_ms INTEGER NOT NULL,
		went_ms INTEGER
	 );
	 CREATE INDEX events_open ON events (plc_id, went_ms);
	 CREATE INDEX events_came ON events (came_ms);`,

	// 3: шунтирование аварий — временно убрать с панели долго висящую аварию.
	//    Шунт действует до until_ms; removed_ms — сняли раньше срока вручную.
	`CREATE TABLE shelves (
		id         INTEGER PRIMARY KEY,
		plc_id     INTEGER NOT NULL REFERENCES plcs(id),
		signal     TEXT    NOT NULL,
		reason     TEXT    NOT NULL,
		who        TEXT    NOT NULL,
		from_ms    INTEGER NOT NULL,
		until_ms   INTEGER NOT NULL,
		removed_ms INTEGER,
		removed_by TEXT    NOT NULL DEFAULT ''
	 );
	CREATE INDEX shelves_plc ON shelves (plc_id, until_ms);`,

	// 4: нормы аналоговых сигналов — границы и скачок (limits.go)
	limitsSchema,

	// 5: скрыть сигнал с графиков Live, не прекращая опрос и запись
	`ALTER TABLE signals ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;`,
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema + snapshotSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("создание таблиц: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate доводит базу до последней версии схемы
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("версия базы: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("база версии %d новее программы (знает до %d) — обновите программу",
			version, len(migrations))
	}

	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("миграция %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("миграция %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("миграция %d: %w", i+1, err)
		}
		log.Printf("база: выполнена миграция %d", i+1)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// ---------- Чтение ----------

func (s *Store) PLCs(ctx context.Context) ([]PLC, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, ip, rack, slot FROM plcs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PLC
	for rows.Next() {
		var p PLC
		if err := rows.Scan(&p.ID, &p.Name, &p.IP, &p.Rack, &p.Slot); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PLC(ctx context.Context, id int64) (PLC, error) {
	var p PLC
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, ip, rack, slot FROM plcs WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.IP, &p.Rack, &p.Slot)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

const signalCols = `id, plc_id, name, addr, type, period_ms, scale, bias, unit, enabled, kind, message, hidden`

type scanner interface {
	Scan(dest ...any) error
}

func scanSignal(sc scanner) (SignalRow, error) {
	var r SignalRow
	err := sc.Scan(&r.ID, &r.PLCID, &r.Name, &r.Addr, &r.Type,
		&r.PeriodMs, &r.Scale, &r.Bias, &r.Unit, &r.Enabled, &r.Kind, &r.Message, &r.Hidden)
	return r, err
}

func (s *Store) Signals(ctx context.Context, plcID int64) ([]SignalRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+signalCols+` FROM signals WHERE plc_id = ? ORDER BY id`, plcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SignalRow
	for rows.Next() {
		r, err := scanSignal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Signal(ctx context.Context, id int64) (SignalRow, error) {
	r, err := scanSignal(s.db.QueryRowContext(ctx,
		`SELECT `+signalCols+` FROM signals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Store) ChangeCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM changelog`).Scan(&n)
	return n, err
}

// ---------- Изменения (каждое — вместе с записью в журнал) ----------

func (s *Store) AddPLC(ctx context.Context, who string, p PLC) (int64, error) {
	return s.change(ctx, who, "добавлен ПЛК",
		fmt.Sprintf("%s (%s, слот %d)", p.Name, p.IP, p.Slot),
		`INSERT INTO plcs (name, ip, rack, slot) VALUES (?, ?, ?, ?)`,
		p.Name, p.IP, p.Rack, p.Slot)
}

func (s *Store) UpdatePLC(ctx context.Context, who string, old, p PLC) error {
	_, err := s.change(ctx, who, "изменён ПЛК",
		fmt.Sprintf("%s (%s, rack %d, slot %d) → %s (%s, rack %d, slot %d)",
			old.Name, old.IP, old.Rack, old.Slot, p.Name, p.IP, p.Rack, p.Slot),
		`UPDATE plcs SET name = ?, ip = ?, rack = ?, slot = ? WHERE id = ?`,
		p.Name, p.IP, p.Rack, p.Slot, p.ID)
	return err
}

func (s *Store) RenamePLC(ctx context.Context, who string, id int64, oldName, newName string) error {
	_, err := s.change(ctx, who, "переименован ПЛК",
		fmt.Sprintf("%s → %s", oldName, newName),
		`UPDATE plcs SET name = ? WHERE id = ?`, newName, id)
	return err
}

// normalize — пустой тип означает «тренд» (так было у всех сигналов до миграции 1)
func normalize(r SignalRow) SignalRow {
	if r.Kind == "" {
		r.Kind = KindTrend
	}
	return r
}

func (s *Store) AddSignal(ctx context.Context, who string, r SignalRow) (int64, error) {
	r = normalize(r)
	return s.change(ctx, who, "добавлен сигнал", describe(r),
		`INSERT INTO signals (plc_id, name, addr, type, period_ms, scale, bias, unit, enabled, kind, message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.PLCID, r.Name, r.Addr, r.Type, r.PeriodMs, r.Scale, r.Bias, r.Unit, r.Enabled, r.Kind, r.Message)
}

func (s *Store) UpdateSignal(ctx context.Context, who string, r SignalRow) error {
	r = normalize(r)
	_, err := s.change(ctx, who, "изменён сигнал", describe(r),
		`UPDATE signals SET name = ?, addr = ?, type = ?, period_ms = ?,
		        scale = ?, bias = ?, unit = ?, kind = ?, message = ? WHERE id = ?`,
		r.Name, r.Addr, r.Type, r.PeriodMs, r.Scale, r.Bias, r.Unit, r.Kind, r.Message, r.ID)
	return err
}

func (s *Store) SetSignalEnabled(ctx context.Context, who string, r SignalRow) error {
	action := "выключен сигнал"
	if r.Enabled {
		action = "включён сигнал"
	}
	_, err := s.change(ctx, who, action, r.Name,
		`UPDATE signals SET enabled = ? WHERE id = ?`, r.Enabled, r.ID)
	return err
}

// SetSignalHidden — скрыть сигнал с графиков Live или вернуть. Опрос и запись не меняются.
func (s *Store) SetSignalHidden(ctx context.Context, who string, r SignalRow) error {
	action := "сигнал показан на графиках"
	if r.Hidden {
		action = "сигнал скрыт с графиков"
	}
	_, err := s.change(ctx, who, action, r.Name,
		`UPDATE signals SET hidden = ? WHERE id = ?`, r.Hidden, r.ID)
	return err
}

func (s *Store) DeleteSignal(ctx context.Context, who string, r SignalRow) error {
	_, err := s.change(ctx, who, "удалён сигнал", describe(r),
		`DELETE FROM signals WHERE id = ?`, r.ID)
	return err
}

func describe(r SignalRow) string {
	d := fmt.Sprintf("%s [%s]: %s %s, %d мс, ×%g %+g %s",
		r.Name, r.Kind, r.Addr, r.Type, r.PeriodMs, r.Scale, r.Bias, r.Unit)
	if r.Message != "" {
		d += " — «" + r.Message + "»"
	}
	return d
}

func (s *Store) change(ctx context.Context, who, action, details, query string, args ...any) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		return 0, ErrNotFound
	}
	id, _ := res.LastInsertId()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO changelog (ts, who, action, details) VALUES (?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), who, action, details); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return id, nil
}
