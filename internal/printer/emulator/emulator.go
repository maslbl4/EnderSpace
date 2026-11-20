// Package emulator — in-memory реализация printer.Printer: разработка и
// проверка EnderSpace (таблица, обогащение, печать, удаление) без
// реального принтера. Включается переменной ENDERSPACE_EMULATOR=1 (dev)
// или флагом -emulator (собранное exe); IP из формы подключения
// принимается любой.
package emulator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"ender-space/internal/printer"
)

// Статическая проверка: Emulator реализует printer.Printer.
var _ printer.Printer = (*Emulator)(nil)

// Дефолты.
const (
	// DefaultPrintDuration — длительность «печати»: ускорена до минуты
	// (на реальном принтере — часы), полный цикл виден за 1 минуту.
	DefaultPrintDuration = time.Minute
	// DefaultTick — период, с которым WatchStatus шлёт состояние.
	DefaultTick = 500 * time.Millisecond
)

// EnvPrintMS — длительность «печати» в миллисекундах (override в dev,
// например ENDERSPACE_EMU_PRINT_MS=10000 — печать заканчивается за 10 с).
const EnvPrintMS = "ENDERSPACE_EMU_PRINT_MS"

// Emulator — фейковый принтер F005: файлы и состояние в памяти, сети нет.
type Emulator struct {
	mu sync.Mutex

	model    string
	firmware string

	files  []printer.File
	bodies map[string][]byte // name → тело G-кода (синтетический заголовок)
	thumbs map[string][]byte // name → PNG превью (нет — 404)

	printing   bool
	printFile  string
	printTimer *time.Timer

	printDuration time.Duration
	tick          time.Duration
}

// Option — настройка эмулятора.
type Option func(*Emulator)

// WithPrintDuration — длительность «печати» (0 — дефолт, 1 минута).
func WithPrintDuration(d time.Duration) Option {
	return func(e *Emulator) {
		if d > 0 {
			e.printDuration = d
		}
	}
}

// WithTick — период обновления состояния (0 — дефолт, 500 мс).
func WithTick(d time.Duration) Option {
	return func(e *Emulator) {
		if d > 0 {
			e.tick = d
		}
	}
}

// WithFiles — подмена списка файлов (тела и превью генерируются для всех;
// файл «без превью» — если в имени есть "nothumb").
func WithFiles(files ...printer.File) Option {
	return func(e *Emulator) {
		e.files = files
	}
}

// New создаёт эмулятор: ~8 образцовых G-кодов, модель F005, прошивка
// V1.1.0.15. Длительность «печати» — 1 минута, переопределяется
// ENDERSPACE_EMU_PRINT_MS (мс).
func New(opts ...Option) *Emulator {
	e := &Emulator{
		model:         "F005",
		firmware:      "V1.1.0.15",
		printDuration: DefaultPrintDuration,
		tick:          DefaultTick,
	}
	if ms := os.Getenv(EnvPrintMS); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 {
			e.printDuration = time.Duration(v) * time.Millisecond
		}
	}
	e.files = defaultFiles()
	for _, o := range opts {
		o(e)
	}
	e.bodies = make(map[string][]byte, len(e.files))
	e.thumbs = make(map[string][]byte, len(e.files))
	for _, f := range e.files {
		e.bodies[f.Name] = synthGcode(f)
		if !strings.Contains(f.Name, "nothumb") {
			e.thumbs[f.Name] = thumbPNG
		}
	}
	return e
}

// DriverName — имя драйвера в конфиге (lastDriver).
func (e *Emulator) DriverName() string { return "emulator" }

// Info — модель и MAC.
func (e *Emulator) Info(ctx context.Context) (printer.Info, error) {
	return printer.Info{MAC: "EMU000000001", Model: e.model}, nil
}

// ListFiles — копия текущего списка.
func (e *Emulator) ListFiles(ctx context.Context) ([]printer.File, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]printer.File, len(e.files))
	copy(out, e.files)
	return out, nil
}

// DeleteFile — реально удаляет файл из списка (операция синхронная;
// гвард «файл печатается» проверяет слой приложения, как и с принтером).
func (e *Emulator) DeleteFile(ctx context.Context, path, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, f := range e.files {
		if f.Path+"/"+f.Name == path+"/"+name {
			e.files = append(e.files[:i], e.files[i+1:]...)
			delete(e.bodies, name)
			delete(e.thumbs, name)
			return nil
		}
	}
	return fmt.Errorf("emulator: файл не найден: %s/%s", path, name)
}

// PrintFile — запускает «печать»: статус немедленно становится Printing,
// через printDuration эмулятор «допечатывает» сам. Подтверждение
// (аппликация ждёт его в StartPrint) приходит следующим тиком WatchStatus.
func (e *Emulator) PrintFile(ctx context.Context, path, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.printing {
		return fmt.Errorf("emulator: уже печатает: %s", e.printFile)
	}
	e.printing = true
	e.printFile = path + "/" + name
	if e.printTimer != nil {
		e.printTimer.Stop()
	}
	e.printTimer = time.AfterFunc(e.printDuration, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.printing = false
		e.printFile = ""
		e.printTimer = nil
	})
	return nil
}

// DownloadGcode — синтетический G-код: заголовок слайсера (время, длина
// филамента, плотность), чтобы обогащение шло по тем же путям, что на
// реальном принтере.
func (e *Emulator) DownloadGcode(ctx context.Context, path, name string) (io.ReadCloser, error) {
	e.mu.Lock()
	body, ok := e.bodies[name]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("emulator: gcode не найден: %s: %w", name, printer.ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// DownloadThumbnail — вшитый 1×1 PNG; 404 у файлов без превью.
func (e *Emulator) DownloadThumbnail(ctx context.Context, path, name string) (io.ReadCloser, error) {
	e.mu.Lock()
	png, ok := e.thumbs[name]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("emulator: превью не найдено: %s: %w", name, printer.ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(png)), nil
}

// WatchStatus — сразу шлёт текущий статус, затем повторяет каждый tick,
// пока не отменён ctx.
func (e *Emulator) WatchStatus(ctx context.Context, fn func(printer.DeviceStatus)) error {
	fn(e.status())
	t := time.NewTicker(e.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			fn(e.status())
		}
	}
}

// status — снимок текущего состояния.
func (e *Emulator) status() printer.DeviceStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	ds := printer.DeviceStatus{Firmware: e.firmware}
	if e.printing {
		ds.Printing = true
		ds.PrintFile = e.printFile
	}
	return ds
}

// synthGcode — синтетический G-код: заголовок слайсера (время и длина
// филамента считаются от размера файла: 1 МБ ≈ 1 с «печати») + пара
// команд. Парсер заголовка останавливается на первой G-команде.
func synthGcode(f printer.File) []byte {
	secs := int(f.Size / 1000)
	var b strings.Builder
	b.WriteString("; generated by EnderSpace emulator\n")
	fmt.Fprintf(&b, "; estimated printing time (normal mode) = %dh %dm %ds\n",
		secs/3600, (secs%3600)/60, secs%60)
	if f.FilamentMM > 0 {
		fmt.Fprintf(&b, "; filament used [mm] = %d\n", f.FilamentMM)
	}
	b.WriteString("; filament_density: 1.24\n")
	b.WriteString("G28\nG29\nG1 X0 Y0 Z0.2 F6000\n")
	return []byte(b.String())
}

// defaultFiles — образцовый список: разные размеры/даты/слои, файл в
// подпапке, файл без превью (404), файл без длины филамента («—» в UI).
func defaultFiles() []printer.File {
	now := time.Now().Unix()
	dir := "/usr/data/printer_data/gcodes"
	return []printer.File{
		{Path: dir, Name: "benchy_PLA_1h15m.gcode", Size: 8_400_000, Layer: 0.2, Epoch: now - 3600, FilamentMM: 2100},
		{Path: dir, Name: "vase_spiralize.gcode", Size: 4_200_000, Layer: 0.28, Epoch: now - 3*24*3600, FilamentMM: 950},
		{Path: dir, Name: "OrcaSlicer_benchy_2h30m.gcode", Size: 12_800_000, Layer: 0.16, Epoch: now - 6*3600, FilamentMM: 3400},
		{Path: dir + "/bench", Name: "calibration_cube.gcode", Size: 610_000, Layer: 0.2, Epoch: now - 12*3600, FilamentMM: 210},
		{Path: dir + "/bench", Name: "nothumb_raw_dump.gcode", Size: 990_000, Layer: 0.2, Epoch: now - 30*24*3600, FilamentMM: 300},
		{Path: dir, Name: "holder_unknown_length.gcode", Size: 3_100_000, Layer: 0.2, Epoch: now - 2*3600, FilamentMM: 0},
		{Path: dir, Name: "big_bracket_PLA_6h45m.gcode", Size: 47_000_000, Layer: 0.2, Epoch: now - 7*24*3600, FilamentMM: 11_200},
		{Path: dir, Name: "test_single_layer.gcode", Size: 120_000, Layer: 0.3, Epoch: now - 45*60, FilamentMM: 80},
	}
}

// thumbPNG — вшитое 1×1 PNG (проверка data URI в UI, размер не важен).
var thumbPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}
