// Package app — сервисный слой EnderSpace: связывает UI (Wails-биндинги)
// с принтером через printer.Client и gcode-парсер. Не зависит от Wails,
// чтобы быть тестируемым.
package app

import (
	"context"
	"encoding/base64"
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
	DownloadThumbnail(ctx context.Context, name string) (io.ReadCloser, error)
	WatchState(ctx context.Context, fn func(*printer.State)) error
}

// gcodeHeadBytes — сколько читать с G-кода: весь заголовок слайсера в
// первых килобайтах, а принтер Range игнорирует и шлёт весь файл.
const gcodeHeadBytes = 256 * 1024

// thumbLimit — превью принтера маленькие (96×96/300×300 PNG), 1 МБ с запасом.
const thumbLimit = 1 << 20

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
	thumbCache    map[string]string // «path|name» → data URI ("" — нет)
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
		thumbCache:    map[string]string{},
	}
}

// Connect подключается к принтеру по IP (http://<ip>), проверяет /info,
// запускает наблюдение за состоянием и запоминает IP в конфиге.
func (a *App) Connect(ctx context.Context, ip string) error {
	ip = normalizeIP(ip)
	client := a.clientFactory(ip)
	info, err := client.Info(ctx)
	if err != nil {
		return fmt.Errorf("подключение к %s: %w", ip, err)
	}

	a.mu.Lock()
	a.disconnectLocked()
	a.client = client
	a.ip = ip
	a.model = info.Model // например "F005" → UI покажет "Ender-3 V3 KE"
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
		// «Печатает» только по полю state: printFileName остаётся с прошлой
		// печати и в простое даёт ложную метку. Значения — из statusArr UI
		// принтера: 1="Printing", 5="Printing Paused" (работа идёт).
		if s, ok := a.state.Int("state"); ok {
			st.Printing = s == 1 || s == 5
		}
	}
	return st
}

// Storage — ёмкость памяти: total и system из конфига (оценки «≈», принтер
// df не отдаёт), used = сумма размеров G-кодов, free = total − used − system.
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
	system := a.cfg.System()
	free := total - used - system
	if free < 0 {
		free = 0
	}
	return printer.Storage{Total: total, Used: used, System: system, Free: free}, nil
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

// enrich — FileMeta для одного файла. Данные и их источники (по убыванию
// приоритета):
//   - время: заголовок G-кода → суффикс времени в имени (OrcaSlicer
//     дописывает оценку: «orcs_3_PLA_2h30m.gcode»);
//   - граммы: заголовок G-кода → length из WS-списка × плотность
//     (принтер сам знает длину филамента в каждом файле);
//   - превью: GET /downloads/humbnail/<base>.png → data URI.
//
// Ошибка скачивания/парсинга — файл остаётся в списке с nil («—»).
func (a *App) enrich(ctx context.Context, client PrinterAPI, rf printer.RawFile) printer.FileMeta {
	fm := printer.FileMeta{Path: rf.Path, Name: rf.Name, Size: rf.Size}
	key := rf.Path + "|" + rf.Name + "|" + strconv.FormatInt(rf.Size, 10)

	a.mu.Lock()
	info, cached := a.gcodeCache[key]
	a.mu.Unlock()
	if !cached {
		info = gcode.ApplyFallbacks(rf.Name, rf.FilamentMM, downloadHead(ctx, client, rf.Name))
		a.mu.Lock()
		a.gcodeCache[key] = info
		a.mu.Unlock()
	}
	fm.PrintTime = info.TimeSeconds
	fm.Filament = info.FilamentGrams
	fm.Thumb = a.thumb(ctx, client, rf, info)
	return fm
}

// downloadHead скачивает G-код и возвращает результат парсинга его
// заголовка (nil — не удалось/не нашлось). Принтер Range игнорирует,
// поэтому читаем только gcodeHeadBytes.
func downloadHead(ctx context.Context, client PrinterAPI, name string) gcode.Info {
	rc, err := client.DownloadGcode(ctx, name)
	if err != nil {
		return gcode.Info{}
	}
	defer rc.Close()
	parsed, perr := gcode.Parse(io.LimitReader(rc, gcodeHeadBytes))
	if perr != nil {
		return gcode.Info{}
	}
	return parsed
}

// thumb — data URI превью файла. Источники: вложенный в G-код PNG
// (300×300, качество слайсера) → GET /downloads/humbnail (96×96).
// Ошибка/отсутствие — пустая строка (UI показывает заглушку).
func (a *App) thumb(ctx context.Context, client PrinterAPI, rf printer.RawFile, info gcode.Info) string {
	key := rf.Path + "|" + rf.Name
	a.mu.Lock()
	v, ok := a.thumbCache[key]
	a.mu.Unlock()
	if ok {
		return v
	}
	v = ""
	if len(info.Thumb) > 0 {
		v = "data:image/png;base64," + base64.StdEncoding.EncodeToString(info.Thumb)
	} else if rc, err := client.DownloadThumbnail(ctx, rf.Name); err == nil {
		b, rerr := io.ReadAll(io.LimitReader(rc, thumbLimit))
		rc.Close()
		if rerr == nil {
			v = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		}
	}
	a.mu.Lock()
	a.thumbCache[key] = v
	a.mu.Unlock()
	return v
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
