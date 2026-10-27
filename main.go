package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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

func main() {
	// Параметры запуска. Справка: plc-analyzer.exe -h
	dbPath := flag.String("db", "analyzer.db", "файл базы настроек (ПЛК, сигналы, слепки)")
	addr := flag.String("addr", "127.0.0.1:8080", "адрес веб-сервера; для доступа из сети — :8080")
	admin := flag.String("admin", "", "кроме этого ПК, менять настройки можно с этих адресов: 10.0.0.5,10.0.0.6")
	flag.StringVar(&dataRoot, "data", `D:\plc-data`, "папка для записи данных")
	flag.IntVar(&keepDays, "keep", 30, "сколько дней хранить записи смен")
	flag.Parse()

	log.SetFlags(log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 1. База
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("база: %v", err)
	}
	defer st.Close()

	plcs, err := st.PLCs(ctx)
	if err != nil {
		log.Fatalf("база: %v", err)
	}
	if len(plcs) == 0 {
		if err := seed(ctx, st); err != nil {
			log.Fatalf("начальные настройки: %v", err)
		}
		log.Println("база пустая — создан пример ПЛК 192.168.0.10; укажите свой адрес на странице «ПЛК»")
		if plcs, err = st.PLCs(ctx); err != nil {
			log.Fatalf("база: %v", err)
		}
	}

	reload := make(chan struct{}, 1)
	st.OnChange = func() {
		select {
		case reload <- struct{}{}:
		default:
		}
	}

	// 2. Веб и менеджер ПЛК
	admins, bad := web.ParseAdmins(*admin)
	if len(bad) > 0 {
		log.Fatalf("-admin: не IP-адрес: %v", bad) // опечатку лучше увидеть сразу, а не «почему не пускает»
	}
	srv := &web.Server{
		Store: st, PLC: plcs[0],
		DataRoot: dataRoot, Cache: archive.NewCache(3),
		Admins: admins,
	}
	m := &manager{ctx: ctx, st: st, srv: srv, units: map[int64]*unit{}}

	srv.Units = func(id int64) (plc.Status, archive.Status, bool) {
		if u := m.unit(id); u != nil {
			return u.poller.Status(), u.rec.Status(), true
		}
		return plc.Status{}, archive.Status{}, false
	}

	m.sync()

	go func() {
		log.Printf("веб: http://%s  ·  данные: %s  ·  хранение: %d дней", *addr, dataRoot, keepDays)
		logAccess(*addr, admins)
		if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
			log.Printf("ВЕБ-СЕРВЕР НЕ ЗАПУЩЕН: %v", err)
		}
	}()

	// 3. Главный цикл
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			log.Println("остановка: дописываю данные на диск…")
			m.shutdownAll()
			log.Println("остановлено")
			return
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
