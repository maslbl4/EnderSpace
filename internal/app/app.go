// Package app — сервисный слой EnderSpace: связывает UI (Wails-биндинги)
// с принтером через printer.Client и gcode-парсер. Не зависит от Wails,
// чтобы быть тестируемым.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"ender-space/internal/config"
	"ender-space/internal/gcode"
	"ender-space/internal/printer"
)

// PrinterAPI — то, что App требует от принтера (реализует *printer.Client;
// в тестах — мок).
type PrinterAPI interface {
	Info(ctx context.Context) (printer.Info, error)
	ListFiles(ctx context.Context) ([]printer.RawFile, error)
	DeleteFile(ctx context.Context, p, name string) error
	DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error)
	WatchState(ctx context.Context, fn func(*printer.State)) error
}

// gcodeHeadBytes — сколько читать с G-кода: весь заголовок слайсера в
// первых килобайтах, а принтер Range игнорирует и шлёт весь файл.
const gcodeHeadBytes = 256 * 1024

// enrichWorkers — параллелизм скачивания G-кодов при ListFiles.
const enrichWorkers = 4

// App — состояние подключения к одному принтеру + операции над его памятью.
type App struct {
	mu            sync.Mutex
	cfgDir        string
	cfg           *config.Config
	clientFactory func(ip string) PrinterAPI
	ip            string
	connected     bool
	model         string
	client        PrinterAPI
	state         *printer.State     // последнее смерженное состояние
	stateCancel   context.CancelFunc // остановка WatchState при Disconnect
	gcodeCache    map[string]gcode.Info
}

// NewApp создаёт App, говорящий с реальным принтером по IP.
func NewApp(cfgDir string) *App {
	return NewAppWithClient(cfgDir, func(ip string) PrinterAPI {
		return printer.New("http://" + ip)
	})
}

// NewAppWithClient — то же, но фабрика клиента задаётся снаружи (тесты).
func NewAppWithClient(cfgDir string, factory func(ip string) PrinterAPI) *App {
	cfg, _ := config.Load(cfgDir) // Load не ошибается: нет файла/мусор → дефолты
	return &App{
		cfgDir:        cfgDir,
		cfg:           cfg,
		clientFactory: factory,
		gcodeCache:    map[string]gcode.Info{},
	}
}

// Connect подключается к принтеру по IP (http://<ip>), проверяет /info,
// запускает наблюдение за состоянием и запоминает IP в конфиге.
func (a *App) Connect(ctx context.Context, ip string) error {
	ip = normalizeIP(ip)
	client := a.clientFactory(ip)
	if _, err := client.Info(ctx); err != nil {
		return fmt.Errorf("подключение к %s: %w", ip, err)
	}

	a.mu.Lock()
	a.disconnectLocked()
	a.client = client
	a.ip = ip
	a.model = ""
	a.connected = true
	a.state = printer.NewState()
	watchCtx, cancel := context.WithCancel(context.Background())
	a.stateCancel = cancel
	a.mu.Unlock()

	go a.watchLoop(watchCtx, client)

	// Запоминаем IP последнего принтера (ошибка записи не критична).
	a.cfg.LastIP = ip
	_ = a.cfg.Save(a.cfgDir)
	return nil
}

// Disconnect отключается от принтера и останавливает наблюдение.
func (a *App) Disconnect() error {
	a.mu.Lock()
	a.disconnectLocked()
	a.mu.Unlock()
	return nil
}

// disconnectLocked — сброс подключённого состояния (вызывать под a.mu).
func (a *App) disconnectLocked() {
	if a.stateCancel != nil {
		a.stateCancel()
		a.stateCancel = nil
	}
	a.connected = false
	a.client = nil
	a.state = nil
	a.model = ""
}

// watchLoop держит WS-наблюдение: при обрыве переподключается, пока не
// отменён ctx.
func (a *App) watchLoop(ctx context.Context, client PrinterAPI) {
	for ctx.Err() == nil {
		// Ошибка WatchState (обрыв WS) не важна: ждём паузу и повторяем.
		_ = client.WatchState(ctx, func(s *printer.State) {
			a.mu.Lock()
			a.state = s
			a.mu.Unlock()
		})
		if ctx.Err() != nil {
			return
		}
		// Обрыв WS — короткая пауза перед повтором.
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// Status — текущее состояние подключения.
func (a *App) Status() printer.PrinterStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := printer.PrinterStatus{IP: a.ip, Connected: a.connected, Model: a.model}
	if a.state != nil {
		if name, ok := a.state.Str("printFileName"); ok && name != "" {
			st.Printing = true
		}
	}
	return st
}

// Storage — ёмкость памяти: total из конфига, used = сумма размеров,
// free = total − used (оценка «≈»: eMMC общая с прошивкой).
func (a *App) Storage() (printer.Storage, error) {
	client, ok := a.activeClient()
	if !ok {
		return printer.Storage{}, errors.New("принтер не подключён")
	}
	files, err := client.ListFiles(context.Background())
	if err != nil {
		return printer.Storage{}, err
	}
	var used int64
	for _, f := range files {
		used += f.Size
	}
	total := a.cfg.Total()
	free := total - used
	if free < 0 {
		free = 0
	}
	return printer.Storage{Total: total, Used: used, Free: free}, nil
}

// ListFiles — список файлов, обогащённый временем печати и граммами из
// заголовков G-кода (параллельно, лимит enrichWorkers; кэш по path+size).
func (a *App) ListFiles(ctx context.Context) ([]printer.FileMeta, error) {
	client, ok := a.activeClient()
	if !ok {
		return nil, errors.New("принтер не подключён")
	}
	files, err := client.ListFiles(ctx)
	if err != nil {
		return nil, err
	}

	meta := make([]printer.FileMeta, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < enrichWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				meta[i] = a.enrich(ctx, client, files[i])
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return meta, nil
}

// enrich — FileMeta для одного файла: данные G-кода из кэша или из
// скачивания (первые gcodeHeadBytes). Ошибка скачивания/парсинга — файл
// остаётся в списке с nil («—»).
func (a *App) enrich(ctx context.Context, client PrinterAPI, rf printer.RawFile) printer.FileMeta {
	fm := printer.FileMeta{Path: rf.Path, Name: rf.Name, Size: rf.Size}
	key := rf.Path + "|" + strconv.FormatInt(rf.Size, 10)

	a.mu.Lock()
	info, cached := a.gcodeCache[key]
	a.mu.Unlock()
	if !cached {
		info = gcode.Info{}
		if rc, err := client.DownloadGcode(ctx, rf.Name); err == nil {
			if parsed, perr := gcode.Parse(io.LimitReader(rc, gcodeHeadBytes)); perr == nil {
				info = parsed
			}
			rc.Close()
		}
		a.mu.Lock()
		a.gcodeCache[key] = info
		a.mu.Unlock()
	}
	fm.PrintTime = info.TimeSeconds
	fm.Filament = info.FilamentGrams
	return fm
}

// DeleteFiles удаляет файлы последовательно. paths — полные пути
// «<path>/<name>». Если не все удалены — ошибка с отчётом «deleted k of n».
func (a *App) DeleteFiles(ctx context.Context, paths []string) error {
	client, ok := a.activeClient()
	if !ok {
		return errors.New("принтер не подключён")
	}
	var done int
	var firstErr error
	for _, p := range paths {
		dir, name := path.Split(p) // dir всегда с завершающим "/"
		if err := client.DeleteFile(ctx, strings.TrimSuffix(dir, "/"), name); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", p, err)
			}
			continue
		}
		done++
	}
	if firstErr != nil {
		// Сообщение на английском: фронтенд сам локализует отчёт по числам.
		return fmt.Errorf("deleted %d of %d: %v", done, len(paths), firstErr)
	}
	return nil
}

// activeClient возвращает активный клиент; ok=false — если не подключены.
func (a *App) activeClient() (PrinterAPI, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client, a.connected
}

// normalizeIP убирает scheme и завершающие "/" из пользовательского ввода.
func normalizeIP(ip string) string {
	ip = strings.TrimSpace(ip)
	ip = strings.TrimPrefix(ip, "http://")
	ip = strings.TrimPrefix(ip, "https://")
	return strings.TrimRight(ip, "/")
}
