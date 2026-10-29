package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KudinovArtemilus/plc-analyzer/internal/archive"
	"github.com/KudinovArtemilus/plc-analyzer/internal/plc"
	"github.com/KudinovArtemilus/plc-analyzer/internal/store"
	"github.com/KudinovArtemilus/plc-analyzer/internal/web"
)

const window = 10 * time.Minute // глубина буфера Live

// Настройки запуска — задаются флагами командной строки (см. main)
var (
	dataRoot string
	keepDays int
)

// manager держит по блоку на каждый ПЛК из базы и сверяет их с базой
type manager struct {
	ctx context.Context
	st  *store.Store
	srv *web.Server

	mu    sync.RWMutex
	units map[int64]*unit
}

func (m *manager) unit(id int64) *unit {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.units[id]
}

func (m *manager) start(pl store.PLC) {
	id := pl.ID
	onConfig := func(gen int, groups []web.Group) {
		m.srv.SetConfig(id, gen, groups)
	}
	u := startUnit(m.ctx, m.st, pl, onConfig)
	m.mu.Lock()
	m.units[pl.ID] = u
	m.mu.Unlock()
	log.Printf("ПЛК %s (%s, rack %d, slot %d): запущен", pl.Name, pl.IP, pl.Rack, pl.Slot)
}

func (m *manager) sync() {
	plcs, err := m.st.PLCs(m.ctx)
	if err != nil {
		log.Printf("менеджер: не удалось прочитать ПЛК: %v", err)
		return
	}

	seen := make(map[int64]bool, len(plcs))
	for _, pl := range plcs {
		seen[pl.ID] = true
		u := m.unit(pl.ID)
		switch {
		case u == nil:
			m.start(pl)
		case u.connChanged(pl):
			log.Printf("ПЛК %s: изменены IP/rack/slot — переподключаюсь", pl.Name)
			u.shutdown()
			m.start(pl)
		default:
			u.notify()
		}
	}

	m.mu.RLock()
	var gone []int64
	for id := range m.units {
		if !seen[id] {
			gone = append(gone, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range gone {
		u := m.unit(id)
		log.Printf("ПЛК %s удалён из базы — останавливаю", u.settings().Name)
		u.shutdown()
		m.mu.Lock()
		delete(m.units, id)
		m.mu.Unlock()
	}
}

func (m *manager) shutdownAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.units {
		u.cancel()
	}
	for _, u := range m.units {
		<-u.done
	}
}

// options — параметры запуска из командной строки
type options struct {
	db, addr, admin, log string
	python, scripts      string // анализ по паспортам на Python (34d)
}

// parseFlags разбирает параметры запуска. Справка: plc-analyzer.exe -h
// Свой FlagSet, а не общий flag.Parse: те же флаги разбирает и команда install —
// проверить их до того, как записать в настройки службы.
func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("plc-analyzer", flag.ContinueOnError)
	fs.StringVar(&o.db, "db", "analyzer.db", "файл базы настроек (ПЛК, сигналы, слепки)")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8080", "адрес веб-сервера; для доступа из сети — :8080")
	fs.StringVar(&o.admin, "admin", "", "кроме этого ПК, менять настройки можно с этих адресов: 10.0.0.5,10.0.0.6")
	fs.StringVar(&o.log, "log", "", `файл лога; по умолчанию plc-analyzer.log в папке -data; "-" — только консоль`)
	fs.StringVar(&dataRoot, "data", `D:\plc-data`, "папка для записи данных")
	fs.IntVar(&keepDays, "keep", 30, "сколько дней хранить записи смен")
	fs.StringVar(&o.python, "python", "", `python.exe для анализа по паспортам; для службы — полный путь, например D:\plc-analyzer\scripts\.venv\Scripts\python.exe`)
	fs.StringVar(&o.scripts, "scripts", "scripts", "папка со скриптами анализа (trend.py, anomaly.py); у службы — рядом с exe")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `PLC Analyzer

  plc-analyzer.exe [параметры]            — работа в окне (Ctrl+C — остановка)
  plc-analyzer.exe install [параметры]    — установить службу Windows с этими параметрами
  plc-analyzer.exe uninstall | start | stop | status

Параметры:`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("лишнее в командной строке: %v (параметры пишутся через дефис: -addr :8080)", fs.Args())
	}

	// Лог по умолчанию — рядом с записями: одно место для всего, что пишет анализатор,
	// и вне папки с кодом (в логе IP-адреса ПЛК — на GitHub ему нельзя).
	// Это файл в корне папки данных, а не папка: папки там — это ПЛК.
	if o.log == "" {
		o.log = filepath.Join(dataRoot, "plc-analyzer.log")
	}
	return o, nil
}

// main решает, КАК запущена программа:
//   - «plc-analyzer.exe install …» и другие команды — управление службой, и сразу выход;
//   - запущена Windows как служба — работа под управлением диспетчера служб;
//   - иначе — работа в окне до Ctrl+C.
//
// Вся работа анализатора — в run, одна и та же во всех режимах.
func main() {
	args := os.Args[1:]
	if len(args) > 0 && isCommand(args[0]) {
		if err := command(args[0], args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
			os.Exit(1)
		}
		return
	}

	opt, err := parseFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return // попросили -h: справку FlagSet уже напечатал
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		os.Exit(2)
	}

	service := isService()
	if service {
		// Служба стартует в C:\Windows\System32. Переходим в папку программы,
		// чтобы относительные пути (-db analyzer.db) значили то же, что при запуске руками.
		if err := os.Chdir(exeDir()); err != nil {
			os.Exit(1) // лога ещё нет, писать некуда; Windows покажет код выхода
		}
	}

	closeLog, err := setupLog(opt.log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "лог %s: %v\n", opt.log, err)
		os.Exit(1)
	}

	if service {
		err = runService(opt)
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		err = run(ctx, opt)
		stop()
	}

	if err != nil {
		log.Printf("ОШИБКА: %v", err)
	}
	closeLog() // log.Fatal здесь не годится: он выходит сразу, и файл лога не закроется
	if err != nil {
		os.Exit(1)
	}
}

// isCommand — первое слово командной строки — команда управления службой?
func isCommand(s string) bool {
	switch s {
	case "install", "uninstall", "start", "stop", "status":
		return true
	}
	return false
}

// exeDir — папка, где лежит plc-analyzer.exe
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// run — вся работа анализатора, пока не отменят ctx. Ошибку запуска возвращает,
// а не завершает программу сама: решать, что с ней делать, — тому, кто вызвал.
func run(ctx context.Context, opt options) error {
	wd, _ := os.Getwd()
	log.Printf("PLC Analyzer: запуск · папка %s · база %s · лог %s", wd, opt.db, opt.log)

	// 1. База
	st, err := store.Open(opt.db)
	if err != nil {
		return fmt.Errorf("база: %w", err)
	}
	defer st.Close()

	plcs, err := st.PLCs(ctx)
	if err != nil {
		return fmt.Errorf("база: %w", err)
	}
	if len(plcs) == 0 {
		if err := seed(ctx, st); err != nil {
			return fmt.Errorf("начальные настройки: %w", err)
		}
		log.Println("база пустая — создан пример ПЛК 192.168.0.10; укажите свой адрес на странице «ПЛК»")
		if plcs, err = st.PLCs(ctx); err != nil {
			return fmt.Errorf("база: %w", err)
		}
	}

	reload := make(chan struct{}, 1)
	st.OnChange = func() {
		select {
		case reload <- struct{}{}:
		default:
		}
	}

	admins, bad := web.ParseAdmins(opt.admin)
	if len(bad) > 0 {
		return fmt.Errorf("-admin: не IP-адрес: %v", bad) // опечатку лучше увидеть сразу, а не «почему не пускает»
	}

	// 2. Порт занимаем ДО запуска опроса. Занят — значит, скорее всего, анализатор
	// уже работает (служба или другое окно). Два анализатора писали бы одни и те же
	// файлы смен, поэтому второй не запускается совсем.
	ln, err := net.Listen("tcp", opt.addr)
	if err != nil {
		return fmt.Errorf("веб %s: %w — возможно, анализатор уже запущен (служба или другое окно)", opt.addr, err)
	}

	// 3. Веб и менеджер ПЛК
	srv := &web.Server{
		Store: st, PLC: plcs[0],
		DataRoot: dataRoot, Cache: archive.NewCache(3),
		Admins: admins,
		Python: opt.python, Scripts: opt.scripts,
	}
	m := &manager{ctx: ctx, st: st, srv: srv, units: map[int64]*unit{}}

	srv.Units = func(id int64) (plc.Status, archive.Status, bool) {
		if u := m.unit(id); u != nil {
			return u.poller.Status(), u.rec.Status(), true
		}
		return plc.Status{}, archive.Status{}, false
	}

	m.sync()

	// Паспорта циклов — в фоне. done закроется, когда фоновая работа завершится:
	// при остановке дождёмся её, иначе база закрылась бы посреди её запроса.
	passportsDone := make(chan struct{})
	go func() {
		defer close(passportsDone)
		runPassports(ctx, st, dataRoot)
	}()

	hs := &http.Server{
		Handler: srv.Handler(),
		// Контекст каждого запроса — дочерний от ctx. При остановке он отменяется,
		// и «вечные» запросы (поток Live, /api/stream) сами завершаются:
		// иначе Shutdown ждал бы их до таймаута.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		log.Printf("веб: http://%s  ·  данные: %s  ·  хранение: %d дней", opt.addr, dataRoot, keepDays)
		logAccess(opt.addr, admins)
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ВЕБ-СЕРВЕР ОСТАНОВЛЕН: %v", err)
		}
	}()

	// 4. Главный цикл
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			log.Println("остановка: дописываю данные на диск…")
			t0 := time.Now()

			// Сначала веб: новые запросы не принимаем, текущие дожидаемся (до 5 с).
			// context.Background, а не ctx: ctx уже отменён, с ним Shutdown не ждал бы ничего.
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := hs.Shutdown(sctx); err != nil {
				log.Printf("веб: остановлен не чисто: %v", err)
			}
			cancel()

			// Потом опрос и запись: файлы смен закрываются целыми
			m.shutdownAll()
			<-passportsDone // паспорт, который считается сейчас, — не дольше одного файла
			log.Printf("остановлено за %v", time.Since(t0).Round(time.Millisecond))
			return nil // база закроется в defer st.Close()
		case <-reload:
			debounce = time.After(time.Second)
		case <-debounce:
			debounce = nil
			m.sync()
		}
	}
}

// seed — пример ПЛК для пустой базы. Без сигналов: их добавляют на странице «Сигналы».
func seed(ctx context.Context, st *store.Store) error {
	_, err := st.AddPLC(ctx, "система", store.PLC{
		Name: "Пример ПЛК", IP: "192.168.0.10", Rack: 0, Slot: 2,
	})
	return err
}

// logAccess — кто может открыть анализатор и кто может менять настройки
func logAccess(addr string, admins map[string]bool) {
	host, _, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		log.Printf("доступ: только с этого ПК (для доступа из сети запустите с -addr :8080)")
		return
	}
	list := make([]string, 0, len(admins))
	for a := range admins {
		list = append(list, a)
	}
	sort.Strings(list)
	who := "только с этого ПК"
	if len(list) > 0 {
		who = "с этого ПК и с " + strings.Join(list, ", ")
	}
	log.Printf("доступ из сети: смотреть — всем; менять настройки — %s", who)
	if ips := localIPs(); len(ips) > 0 {
		log.Printf("с других ПК открывайте: http://%s%s", ips[0], addr[strings.LastIndex(addr, ":"):])
	}
}

// localIPs — IPv4-адреса этого ПК в сети (без 127.0.0.1): подсказка, что вводить на других ПК
func localIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}
