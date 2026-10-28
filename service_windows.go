package main

// Файл собирается только для Windows: суффикс _windows.go — это условие сборки,
// Go сам пропускает файл на других системах (там работает service_other.go).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	svcName    = "PLCAnalyzer" // имя для sc.exe и services.msc (без пробелов)
	svcDisplay = "PLC Analyzer"
	svcDescr   = "Сбор, запись и просмотр данных ПЛК Siemens S7 (веб-интерфейс в браузере)"
)

// ---------- Работа службой ----------

// isService — запустил ли нас диспетчер служб Windows (а не человек из консоли)
func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// runService отдаёт управление диспетчеру служб. Возвращается, когда службу остановили.
func runService(opt options) error {
	return svc.Run(svcName, &service{opt: opt})
}

type service struct{ opt options }

// Execute — то, что диспетчер служб вызывает у службы. Мы сообщаем ему состояние
// (запускается → работает → останавливается) в канал status и слушаем команды из req.
// Сама работа — тот же run, что и в окне; вместо Ctrl+C его останавливает cancel().
func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, s.opt) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Printf("служба %s: работает", svcName)

	for {
		select {
		case err := <-done:
			// run закончился сам, никто не просил: это ошибка (например, порт занят).
			// Ненулевой код выхода — сигнал Windows перезапустить службу (см. install).
			if err != nil {
				log.Printf("служба %s: ОШИБКА: %v", svcName, err)
				return true, 1
			}
			return false, 0

		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus // «ты жив?» — отвечаем текущим состоянием
			case svc.Stop, svc.Shutdown:
				log.Printf("служба %s: команда остановки", svcName)
				// WaitHint — сколько ждать нас, прежде чем считать зависшими
				status <- svc.Status{State: svc.StopPending, WaitHint: 30_000}
				cancel()
				if err := <-done; err != nil {
					log.Printf("служба %s: остановка с ошибкой: %v", svcName, err)
				}
				return false, 0
			}
		}
	}
}

// ---------- Команды: install, uninstall, start, stop, status ----------

func command(cmd string, args []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return explain(err)
	}
	defer m.Disconnect()

	switch cmd {
	case "install":
		return install(m, args)
	case "uninstall":
		return uninstall(m)
	}

	if len(args) > 0 {
		return fmt.Errorf("%s: параметры не нужны (они задаются при install)", cmd)
	}
	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("служба %s не установлена (plc-analyzer.exe install …)", svcName)
	}
	defer s.Close()

	switch cmd {
	case "start":
		if err := s.Start(); err != nil {
			return explain(err)
		}
		return wait(s, svc.Running)
	case "stop":
		if _, err := s.Control(svc.Stop); err != nil {
			return explain(err)
		}
		return wait(s, svc.Stopped)
	default: // status
		st, err := s.Query()
		if err != nil {
			return explain(err)
		}
		cfg, _ := s.Config()
		fmt.Printf("служба %s: %s\nкоманда: %s\n", svcName, stateName(st.State), cfg.BinaryPathName)
		return nil
	}
}

// install регистрирует службу: автозапуск, перезапуск после сбоя, параметры запуска.
// Параметры (-addr, -admin…) сначала проверяем тем же parseFlags: опечатку лучше
// увидеть сейчас, чем в логе службы после перезагрузки.
func install(m *mgr.Mgr, args []string) error {
	if _, err := parseFlags(args); err != nil {
		return err
	}
	if s, err := m.OpenService(svcName); err == nil {
		s.Close()
		return fmt.Errorf("служба %s уже установлена; чтобы поменять параметры: stop, uninstall, install", svcName)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	s, err := m.CreateService(svcName, exe, mgr.Config{
		DisplayName: svcDisplay,
		Description: svcDescr,
		StartType:   mgr.StartAutomatic,
		// Отложенный автозапуск — через минуту-две после загрузки Windows,
		// когда сеть уже поднята: иначе первые попытки связи с ПЛК уйдут в пустоту.
		DelayedAutoStart: true,
	}, args...) // параметры попадут в командную строку службы
	if err != nil {
		return explain(err)
	}
	defer s.Close()

	// Перезапуск после сбоя: через 10 с, 30 с, потом каждую минуту.
	// Счётчик сбоев обнуляется через сутки без сбоев.
	err = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}, uint32((24 * time.Hour).Seconds()))
	if err == nil {
		// Перезапускать не только при падении процесса, но и когда служба
		// сама остановилась с ошибкой (Execute вернул код 1)
		err = s.SetRecoveryActionsOnNonCrashFailures(true)
	}
	if err != nil {
		return fmt.Errorf("служба установлена, но перезапуск после сбоя не настроен: %w", err)
	}

	fmt.Printf("служба %s установлена: %s %s\n", svcName, exe, strings.Join(args, " "))
	fmt.Println("автозапуск с Windows (отложенный), перезапуск после сбоя")
	fmt.Println("запустить сейчас: plc-analyzer.exe start")
	return nil
}

func uninstall(m *mgr.Mgr) error {
	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("служба %s не установлена", svcName)
	}
	defer s.Close()

	// Работает — сначала останавливаем, чтобы файлы смен закрылись целыми
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err == nil {
			wait(s, svc.Stopped)
		}
	}
	if err := s.Delete(); err != nil {
		return explain(err)
	}
	fmt.Printf("служба %s удалена (база, лог и записи на месте)\n", svcName)
	return nil
}

// wait ждёт нужного состояния службы до 40 секунд, печатая точки
func wait(s *mgr.Service, want svc.State) error {
	deadline := time.Now().Add(40 * time.Second)
	for {
		st, err := s.Query()
		if err != nil {
			return explain(err)
		}
		if st.State == want {
			fmt.Printf("\nслужба %s: %s\n", svcName, stateName(want))
			return nil
		}
		if st.State == svc.Stopped && want == svc.Running {
			return fmt.Errorf("служба не запустилась — причина в логе (по умолчанию D:\\plc-data\\plc-analyzer.log)")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("служба не перешла в состояние «%s» за 40 с (сейчас: %s)", stateName(want), stateName(st.State))
		}
		fmt.Print(".")
		time.Sleep(500 * time.Millisecond)
	}
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "остановлена"
	case svc.StartPending:
		return "запускается"
	case svc.StopPending:
		return "останавливается"
	case svc.Running:
		return "работает"
	}
	return fmt.Sprintf("состояние %d", s)
}

// explain добавляет подсказку к самой частой ошибке: команды служб требуют прав администратора
func explain(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w — запустите PowerShell от имени администратора", err)
	}
	return err
}
