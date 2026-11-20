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
	"sync/atomic"
	"time"

	"ender-space/internal/config"
	"ender-space/internal/gcode"
	"ender-space/internal/printer"
)

// EventSink — отправка событий в фронтенд (в main — Wails-рантайм;
// в тестах — коллектор). nil — события не шлём.
type EventSink func(name string, data any)

// Имена событий обогащения списка файлов (фаза 2).
const (
	filesProgressEvent = "files:progress" // data: EnrichProgress
	filesEnrichedEvent = "files:enriched" // data: printer.FileMeta
	filesDoneEvent     = "files:done"     // data: nil — все файлы обработаны
)

// EnrichProgress — прогресс фонового обогащения (обработано/всего).
// Без json-тегов: в событии поля уходят как Done/Total (как в FileMeta).
type EnrichProgress struct {
	Done  int
	Total int
}

// gcodeHeadBytes — сколько читать с G-кода: весь заголовок слайсера в
// первых килобайтах, а принтер Range игнорирует и шлёт весь файл.
const gcodeHeadBytes = 256 * 1024

// thumbLimit — превью принтера маленькие (96×96/300×300 PNG), 1 МБ с запасом.
const thumbLimit = 1 << 20

// enrichWorkers — параллелизм скачивания G-кодов при фоновом обогащении.
// 4, а не 8: при 8 параллельных докачках eMMC-принтер «захлёбывался»
// (минуты таймаутов /info и WS после bursts из 58 файлов).
const enrichWorkers = 4

// retryDelays — паузы перед ретраями докачки (3 ретрая → 4 попытки).
// var, а не const: тесты подменяют на миллисекунды.
var retryDelays = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

// App — состояние подключения к одному принтеру + операции над его памятью.
type App struct {
	mu            sync.Mutex
	cfgDir        string
	cfg           *config.Config
	clientFactory func(ip string) printer.Printer
	sink          EventSink // события для фронтенда (nil — не шлём)
	ip            string
	connected     bool
	model         string
	client        printer.Printer
	status        printer.DeviceStatus // последнее состояние от драйвера
	stateCancel   context.CancelFunc   // остановка WatchStatus при Disconnect
	enrichCancel  context.CancelFunc   // остановка фонового обогащения
	gcodeCache    map[string]gcode.Info
	thumbCache    map[string]string // «path|name» → data URI ("" — нет)
}

// NewApp создаёт App, говорящий с реальным принтером по IP (без событий).
func NewApp(cfgDir string) *App {
	return NewAppWithSink(cfgDir, nil)
}

// NewAppWithSink — то же, но события обогащения уходят в sink
// (в main — Wails-рантайм). Драйвер (стоковая прошивка Creality или
// NebulaOS/Moonraker) определяется автоматически при подключении.
func NewAppWithSink(cfgDir string, sink EventSink) *App {
	return NewAppWithClientAndSink(cfgDir, detectDriver, sink)
}

// NewAppWithClient — то же, но фабрика принтера задаётся снаружи (тесты).
func NewAppWithClient(cfgDir string, factory func(ip string) printer.Printer) *App {
	return NewAppWithClientAndSink(cfgDir, factory, nil)
}

// NewAppWithClientAndSink — полный конструктор: фабрика принтера + события.
// Фабрика — любой драйвер printer.Printer: creality (реальный F005),
// emulator (отладка без железа) и будущие.
func NewAppWithClientAndSink(cfgDir string, factory func(ip string) printer.Printer, sink EventSink) *App {
	cfg, _ := config.Load(cfgDir) // Load не ошибается: нет файла/мусор → дефолты
	return &App{
		cfgDir:        cfgDir,
		cfg:           cfg,
		clientFactory: factory,
		sink:          sink,
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
	a.status = printer.DeviceStatus{}
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
	if a.enrichCancel != nil {
		a.enrichCancel()
		a.enrichCancel = nil
	}
	a.connected = false
	a.client = nil
	a.status = printer.DeviceStatus{}
	a.model = ""
}

// watchLoop держит наблюдение за состоянием: при обрыве переподключается,
// пока не отменён ctx.
func (a *App) watchLoop(ctx context.Context, client printer.Printer) {
	for ctx.Err() == nil {
		// Ошибка WatchStatus (обрыв связи) не важна: ждём паузу и повторяем.
		_ = client.WatchStatus(ctx, func(ds printer.DeviceStatus) {
			a.mu.Lock()
			a.status = ds
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

// Status — текущее состояние подключения. Поля состояния (печатает/прошивка)
// — из последнего DeviceStatus, который прислал драйвер: нормализация сырых
// полей протокола (state, modelVersion) — ответственность драйвера.
func (a *App) Status() printer.PrinterStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := printer.PrinterStatus{IP: a.ip, Connected: a.connected, Model: a.model}
	st.Printing = a.status.Printing
	st.PrintFile = a.status.PrintFile
	st.FirmwareVersion = a.status.Firmware
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

// ListFiles — фаза 1: мгновенный список из WS (имя, размер; время/граммы/
// превью — из кэша, если уже были). Фаза 2 — фоновое обогащение
// (startEnrich): результаты уходят во фронтенд событиями
// files:enriched / files:progress / files:done, таблица заполняется
// по мере их прихода.
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
	for i, rf := range files {
		meta[i] = a.prefill(rf)
	}
	a.startEnrich(client, files)
	return meta, nil
}

// prefill — FileMeta без сетевых запросов: время/граммы из gcode-кэша,
// превью из thumb-кэша (нет — nil/"", догонят события обогащения).
func (a *App) prefill(rf printer.File) printer.FileMeta {
	fm := printer.FileMeta{Path: rf.Path, Name: rf.Name, Size: rf.Size, Epoch: rf.Epoch}
	a.mu.Lock()
	if info, ok := a.gcodeCache[gcodeCacheKey(rf)]; ok {
		fm.PrintTime = info.TimeSeconds
		fm.Filament = info.FilamentGrams
	}
	fm.Thumb = a.thumbCache[rf.Path+"|"+rf.Name]
	a.mu.Unlock()
	return fm
}

// startEnrich — фаза 2: пул воркеров (enrichWorkers) добирает время/
// граммы/превью и шлёт их событиями. Новая фаза (⟳, повторное ListFiles)
// отменяет предыдущую; отменённая фаза события не шлёт.
func (a *App) startEnrich(client printer.Printer, files []printer.File) {
	total := len(files)

	a.mu.Lock()
	if a.enrichCancel != nil {
		a.enrichCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.enrichCancel = cancel
	a.mu.Unlock()

	if total == 0 {
		cancel()
		a.emit(filesDoneEvent, nil)
		return
	}

	var (
		wg   sync.WaitGroup
		done int32
	)
	// Буфер на все файлы: фидер не блокируется, если воркеры вышли
	// досрочно (отмена фазы) — канал просто уйдёт в GC вместе с фазами.
	jobs := make(chan int, total)
	for i := 0; i < enrichWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				fm := a.enrich(ctx, client, files[i])
				if ctx.Err() != nil {
					return // началась новая фаза — не засоряем её старыми событиями
				}
				a.emit(filesEnrichedEvent, fm)
				a.emit(filesProgressEvent, EnrichProgress{
					Done:  int(atomic.AddInt32(&done, 1)),
					Total: total,
				})
			}
		}()
	}
	go func() {
		for i := range files {
			jobs <- i
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		finished := ctx.Err() == nil // до cancel: фаза не была предвзята новой
		cancel()
		if finished {
			a.emit(filesDoneEvent, nil)
		}
	}()
}

// emit — событие в sink (без sink — молча).
func (a *App) emit(name string, data any) {
	a.mu.Lock()
	sink := a.sink
	a.mu.Unlock()
	if sink != nil {
		sink(name, data)
	}
}

// enrich — FileMeta для одного файла. Данные и их источники (по убыванию
// приоритета):
//   - время: заголовок G-кода → суффикс времени в имени (OrcaSlicer
//     дописывает оценку: «orcs_3_PLA_2h30m.gcode»);
//   - граммы: заголовок G-кода → length из WS-списка × плотность
//     (принтер сам знает длину филамента в каждом файле);
//   - превью: GET /downloads/humbnail/<base>.png → data URI.
//
// Ошибка скачивания (после всех ретраев) — файл остаётся в списке
// с фолбэками (время из имени, граммы из length) или «—», но в кэш НЕ
// уходит: следующий проход (⟳) повторит докачку.
// gcodeCacheKey — ключ gcode-кэша: при изменении размера файла данные
// из заголовка устарели.
func gcodeCacheKey(rf printer.File) string {
	return rf.Path + "|" + rf.Name + "|" + strconv.FormatInt(rf.Size, 10)
}

func (a *App) enrich(ctx context.Context, client printer.Printer, rf printer.File) printer.FileMeta {
	fm := printer.FileMeta{Path: rf.Path, Name: rf.Name, Size: rf.Size, Epoch: rf.Epoch}
	key := gcodeCacheKey(rf)

	a.mu.Lock()
	info, cached := a.gcodeCache[key]
	a.mu.Unlock()
	if !cached {
		var ok bool
		info, ok = a.downloadHeadWithRetries(ctx, client, rf.Path, rf.Name)
		// Фолбэки (время из имени, граммы из length) применяем всегда —
		// они работают и при провале докачки. В кэш уходит только
		// успешно скачанный заголовок.
		info = gcode.ApplyFallbacks(rf.Name, rf.FilamentMM, info)
		if ok {
			a.mu.Lock()
			a.gcodeCache[key] = info
			a.mu.Unlock()
		}
	}
	fm.PrintTime = info.TimeSeconds
	fm.Filament = info.FilamentGrams
	fm.Thumb = a.thumb(ctx, client, rf, info)
	return fm
}

// withRetries — до 4 попыток (1 + 3 ретрая) с паузами retryDelays между
// ними. fn возвращает nil, если попытка удалась. retriable решает, стоит
// ли повторять конкретную ошибку (например, 404 — окончательный ответ).
// Возвращает ошибку последней попытки; nil — успех. Отмена фазы — стоп сразу.
func withRetries(ctx context.Context, retriable func(error) bool, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil || !retriable(lastErr) || attempt >= len(retryDelays) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(retryDelays[attempt]):
		}
	}
}

// anyError — любая ошибка ретраится (сетевые сбои, таймауты).
func anyError(error) bool { return true }

// downloadHeadWithRetries — скачивание заголовка G-кода с ретраями.
// ok=false — все попытки провалились (или фаза отменена).
func (a *App) downloadHeadWithRetries(ctx context.Context, client printer.Printer, path, name string) (gcode.Info, bool) {
	var (
		info gcode.Info
		ok   bool
	)
	err := withRetries(ctx, anyError, func(ctx context.Context) error {
		info, ok = downloadHead(ctx, client, path, name)
		if !ok {
			return errors.New("докачка заголовка не удалась")
		}
		return nil
	})
	return info, err == nil
}

// downloadHead скачивает G-код и возвращает результат парсинга его
// заголовка. ok=false — скачивание/парсинг не удалось. Принтер Range
// игнорирует, поэтому читаем только gcodeHeadBytes.
func downloadHead(ctx context.Context, client printer.Printer, path, name string) (gcode.Info, bool) {
	rc, err := client.DownloadGcode(ctx, path, name)
	if err != nil {
		return gcode.Info{}, false
	}
	defer rc.Close()
	parsed, perr := gcode.Parse(io.LimitReader(rc, gcodeHeadBytes))
	if perr != nil {
		return gcode.Info{}, false
	}
	return parsed, true
}

// thumb — data URI превью файла. Источники: вложенный в G-код PNG
// (300×300, качество слайсера) → GET /downloads/humbnail (96×96).
// Правила кэша:
//   - успех (и 404 — «превью точно нет») — кэшируется, включая "";
//   - сетевой провал (после 4 попыток) — в кэш НЕ уходит: следующий
//     проход (⟳) попробует снова.
func (a *App) thumb(ctx context.Context, client printer.Printer, rf printer.File, info gcode.Info) string {
	key := rf.Path + "|" + rf.Name
	a.mu.Lock()
	v, ok := a.thumbCache[key]
	a.mu.Unlock()
	if ok {
		return v
	}
	if len(info.Thumb) > 0 {
		v = "data:image/png;base64," + base64.StdEncoding.EncodeToString(info.Thumb)
	} else {
		var png []byte
		err := withRetries(ctx,
			// 404 — окончательный ответ «превью нет», ретраить нечего.
			func(err error) bool { return !errors.Is(err, printer.ErrNotFound) },
			func(ctx context.Context) error {
				rc, err := client.DownloadThumbnail(ctx, rf.Path, rf.Name)
				if err != nil {
					return err
				}
				defer rc.Close()
				b, rerr := io.ReadAll(io.LimitReader(rc, thumbLimit))
				if rerr != nil {
					return rerr
				}
				png = b
				return nil
			})
		if err == nil {
			v = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		} else if !errors.Is(err, printer.ErrNotFound) {
			return "" // сетевой провал: без кэша, следующий проход повторит
		}
	}
	a.mu.Lock()
	a.thumbCache[key] = v
	a.mu.Unlock()
	return v
}

// deleteCheckDelay — пауза между отправкой deleteprt и проверкой списка:
// принтер обрабатывает операцию асинхронно, без ack, и под нагрузкой
// (сразу после bursts скачиваний) — заметно дольше. var для тестов.
var deleteCheckDelay = 1500 * time.Millisecond

// deleteMaxRounds — раундов «отправить → подождать → сверить со списком».
// Остаткам каждый раунд шлём deleteprt повторно.
const deleteMaxRounds = 3

// DeleteFiles удаляет файлы и УБЕЖДается в этом: принтер не шлёт ack на
// deleteprt и под нагрузкой обрабатывает операцию с задержкой (или
// пропускает), поэтому после каждого раунда сверяемся с WS-списком и
// остаткам повторяем. paths — полные пути «<path>/<name>».
// Не всё удалилось — ошибка «deleted k of n» (фронтенд локализует по числам).
func (a *App) DeleteFiles(ctx context.Context, paths []string) error {
	client, ok := a.activeClient()
	if !ok {
		return errors.New("принтер не подключён")
	}
	// Печататься файл и удаляться не могут одновременно: принтер «держит»
	// его, и удаление может убить печать. Гвард — на весь список.
	if p := a.printingFile(); p != "" {
		for _, target := range paths {
			if sameFile(p, target) {
				return fmt.Errorf("нельзя удалить %s: файл сейчас печатается", path.Base(target))
			}
		}
	}

	var firstErr error
	fail := func(p string, err error) {
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", p, err)
		}
	}
	send := func(list []string) {
		for _, p := range list {
			dir, name := path.Split(p) // dir всегда с завершающим "/"
			if err := client.DeleteFile(ctx, strings.TrimSuffix(dir, "/"), name); err != nil {
				fail(p, err)
			}
		}
	}

	remaining := paths
	for round := 0; round < deleteMaxRounds; round++ {
		send(remaining)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(deleteCheckDelay):
		}
		files, err := client.ListFiles(ctx)
		if err != nil {
			fail("", err)
			continue // список не сверишь — в следующем раунде повторим остаток
		}
		present := make(map[string]bool, len(files))
		for _, f := range files {
			present[f.Path+"|"+f.Name] = true
		}
		remaining = nil
		for _, p := range paths {
			dir, name := path.Split(p)
			if present[strings.TrimSuffix(dir, "/")+"|"+name] {
				remaining = append(remaining, p)
			}
		}
		if len(remaining) == 0 {
			return nil
		}
	}
	// English on purpose: the frontend localizes the report by the numbers.
	return fmt.Errorf("deleted %d of %d: still on the printer: %v",
		len(paths)-len(remaining), len(paths), remaining)
}

// printConfirmTimeout — сколько ждать подтверждения запуска печати
// (push состояния с printFileName == файл и state ∈ {1,5}). var для тестов.
var printConfirmTimeout = 30 * time.Second

// printPoll — период опроса состояния при ожидании подтверждения. var для
// тестов.
var printPoll = 300 * time.Millisecond

// StartPrint запускает печать одного файла (full — «<path>/<name>») и ждёт
// подтверждения: драйвер, как и при deleteprt, не даёт ack — подтверждением
// служит следующий DeviceStatus, где принтер уже «печатает» и путь совпал
// с файлом.
func (a *App) StartPrint(ctx context.Context, full string) error {
	client, ok := a.activeClient()
	if !ok {
		return errors.New("принтер не подключён")
	}
	if p := a.printingFile(); p != "" {
		return fmt.Errorf("принтер уже печатает: %s", p)
	}
	dir, name := path.Split(full) // dir всегда с завершающим "/"
	if err := client.PrintFile(ctx, strings.TrimSuffix(dir, "/"), name); err != nil {
		return err
	}
	deadline := time.Now().Add(printConfirmTimeout)
	for time.Now().Before(deadline) {
		if a.printingFile() == full {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(printPoll):
		}
	}
	return errors.New("печать не подтвердилась: принтер не перешёл в статус «Печатает»")
}

// printingFile — полный путь печатающегося сейчас файла или "" — принтер
// не печатает. Драйвер заполняет PrintFile только при реальной печати
// (остаточное printFileName из прошлой печати в простое не уйдёт).
func (a *App) printingFile() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status.Printing {
		return a.status.PrintFile
	}
	return ""
}

// sameFile — одно и то же ли это: полные пути равны или равны basename
// (путь из WS-списка и printFileName могут отличаться префиксом).
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return path.Base(a) == path.Base(b)
}

// activeClient возвращает активный принтер; ok=false — если не подключены.
func (a *App) activeClient() (printer.Printer, bool) {
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
