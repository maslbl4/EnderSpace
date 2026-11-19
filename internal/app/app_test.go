package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"ender-space/internal/config"
	"ender-space/internal/printer"
)

// mockPrinter — заглушка PrinterAPI с управляемым поведением.
type mockPrinter struct {
	mu          sync.Mutex
	infoOK      bool
	files       []printer.RawFile
	gcode       map[string][]byte // name → тело G-кода
	thumbs      map[string][]byte // name → PNG превью
	downloadErr map[string]error  // name → ошибка скачивания
	deleteErr   map[string]error  // "path/name" → ошибка удаления
	deleted     []string
}

func (m *mockPrinter) Info(ctx context.Context) (printer.Info, error) {
	if !m.infoOK {
		return printer.Info{}, errors.New("mock: принтер не отвечает")
	}
	return printer.Info{MAC: "TEST", Model: "F005"}, nil
}

func (m *mockPrinter) ListFiles(ctx context.Context) ([]printer.RawFile, error) {
	return m.files, nil
}

func (m *mockPrinter) DeleteFile(ctx context.Context, path, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := path + "/" + name
	if err := m.deleteErr[key]; err != nil {
		return err
	}
	m.deleted = append(m.deleted, key)
	return nil
}

func (m *mockPrinter) DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.downloadErr[name]; err != nil {
		return nil, err
	}
	body, ok := m.gcode[name]
	if !ok {
		return nil, errors.New("mock: файл не найден")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (m *mockPrinter) DownloadThumbnail(ctx context.Context, name string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.thumbs[name]
	if !ok {
		return nil, errors.New("mock: превью не найдено")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// WatchState в тестах молчит (флаги состояния не проверяем).
func (m *mockPrinter) WatchState(ctx context.Context, fn func(*printer.State)) error {
	<-ctx.Done()
	return ctx.Err()
}

func newTestApp(t *testing.T, m *mockPrinter) *App {
	t.Helper()
	return NewAppWithClient(t.TempDir(), func(ip string) PrinterAPI { return m })
}

// loadAppConfig — читает config.json из каталога приложения (тест-хелпер).
func loadAppConfig(a *App) (*config.Config, error) {
	return config.Load(a.cfgDir)
}

func TestListFilesMergesGcodeInfo(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
		gcode: map[string][]byte{
			"a.gcode": []byte("; estimated printing time (normal mode) = 1h 0m 0s\n; total filament used [g] = 10\n"),
			"b.gcode": []byte("; estimated printing time (normal mode) = 2h 0m 0s\n; total filament used [g] = 20\n"),
		},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("len = %d, want 2", len(meta))
	}
	if meta[0].PrintTime == nil || *meta[0].PrintTime != 3600 {
		t.Errorf("a: PrintTime = %v, want 3600", meta[0].PrintTime)
	}
	if meta[0].Filament == nil || *meta[0].Filament != 10 {
		t.Errorf("a: Filament = %v, want 10", meta[0].Filament)
	}
	if meta[1].PrintTime == nil || *meta[1].PrintTime != 7200 {
		t.Errorf("b: PrintTime = %v, want 7200", meta[1].PrintTime)
	}
	if meta[1].Filament == nil || *meta[1].Filament != 20 {
		t.Errorf("b: Filament = %v, want 20", meta[1].Filament)
	}
}

func TestListFilesParserFailureKeepsFile(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
		gcode:       map[string][]byte{"a.gcode": []byte("; total filament used [g] = 5\n")},
		downloadErr: map[string]error{"b.gcode": errors.New("mock: сеть упала")},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ошибка скачивания одного файла не должна ронять список: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("len = %d, want 2", len(meta))
	}
	if meta[1].PrintTime != nil || meta[1].Filament != nil {
		t.Errorf("b: ожидалось «—» (nil), got PrintTime=%v Filament=%v", meta[1].PrintTime, meta[1].Filament)
	}
	if meta[0].Filament == nil || *meta[0].Filament != 5 {
		t.Errorf("a: Filament = %v, want 5", meta[0].Filament)
	}
}

func TestDeleteFilesPartial(t *testing.T) {
	m := &mockPrinter{
		deleteErr: map[string]error{"/p/b.gcode": errors.New("mock: отказ")},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode", "/p/b.gcode"})
	if err == nil {
		t.Fatal("ожидалась ошибка (не все файлы удалены)")
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("отчёт об ошибке = %q, want упоминание «1 of 2»", err.Error())
	}
	if len(m.deleted) != 1 || m.deleted[0] != "/p/a.gcode" {
		t.Errorf("deleted = %v, want [/p/a.gcode]", m.deleted)
	}
}

func TestDeleteFilesAllOK(t *testing.T) {
	m := &mockPrinter{}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	if err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode"}); err != nil {
		t.Fatalf("DeleteFiles: %v", err)
	}
	if len(m.deleted) != 1 || m.deleted[0] != "/p/a.gcode" {
		t.Errorf("deleted = %v", m.deleted)
	}
}

func TestConnectPersistsIP(t *testing.T) {
	m := &mockPrinter{infoOK: true}
	app := newTestApp(t, m)

	if err := app.Connect(context.Background(), "192.168.3.166"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	cfg, err := loadAppConfig(app)
	if err != nil {
		t.Fatalf("конфиг после Connect: %v", err)
	}
	if cfg.LastIP != "192.168.3.166" {
		t.Errorf("config.LastIP = %q, want 192.168.3.166", cfg.LastIP)
	}
	st := app.Status()
	if !st.Connected || st.IP != "192.168.3.166" {
		t.Errorf("Status() = %+v, want Connected, IP=192.168.3.166", st)
	}
	if st.Model != "F005" {
		t.Errorf("Status().Model = %q, want F005 (из /info)", st.Model)
	}
}

func TestConnectFailsWhenPrinterUnreachable(t *testing.T) {
	m := &mockPrinter{infoOK: false}
	app := newTestApp(t, m)
	if err := app.Connect(context.Background(), "10.9.9.9"); err == nil {
		t.Fatal("Connect должен упасть, если принтер не отвечает")
	}
	if app.Status().Connected {
		t.Error("Connected = true после неудачного Connect")
	}
}

// «Печатает» только по полю state (1=печать, 5=пауза); остаточный
// printFileName из прошлой печати в простое ложную метку не даёт.
func TestStatusPrintingByState(t *testing.T) {
	m := &mockPrinter{infoOK: true}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m
	st := printer.NewState()
	app.mu.Lock()
	app.state = st
	app.mu.Unlock()

	cases := []struct {
		push string
		want bool
		what string
	}{
		{`{"state":0,"printFileName":"/p/last.gcode"}`, false, "простой, printFileName остаточный"},
		{`{"state":1}`, true, "печать"},
		{`{"state":5}`, true, "пауза (работа ещё идёт)"},
		{`{"state":2}`, false, "завершено"},
		{`{"state":3}`, false, "сбой"},
	}
	for _, c := range cases {
		_ = st.Merge([]byte(c.push))
		if got := app.Status().Printing; got != c.want {
			t.Errorf("push %s: Printing = %v, want %v (%s)", c.push, got, c.want, c.what)
		}
	}
}

func TestStorageSumsSizes(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	st, err := app.Storage()
	if err != nil {
		t.Fatalf("Storage: %v", err)
	}
	if st.Used != 300 {
		t.Errorf("Used = %d, want 300", st.Used)
	}
	if st.Free != st.Total-300 {
		t.Errorf("Free = %d, want Total-Used = %d", st.Free, st.Total-300)
	}
}

func TestListFilesCachesGcodeInfo(t *testing.T) {
	downloads := 0
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode: map[string][]byte{"a.gcode": []byte("; total filament used [g] = 5\n")},
	}
	app := newTestApp(t, m)
	app.connected = true
	// Подсчёт скачиваний обёрткой
	wrapped := &countingDownloader{m: m, n: &downloads}
	app.client = wrapped

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Errorf("скачиваний = %d, want 1 (кэш по path+size)", downloads)
	}
}

// countingDownloader — считает обращения DownloadGcode.
type countingDownloader struct {
	m *mockPrinter
	n *int
}

func (c *countingDownloader) Info(ctx context.Context) (printer.Info, error) {
	return c.m.Info(ctx)
}
func (c *countingDownloader) ListFiles(ctx context.Context) ([]printer.RawFile, error) {
	return c.m.ListFiles(ctx)
}
func (c *countingDownloader) DeleteFile(ctx context.Context, path, name string) error {
	return c.m.DeleteFile(ctx, path, name)
}
func (c *countingDownloader) DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error) {
	*c.n += 1
	return c.m.DownloadGcode(ctx, name)
}
func (c *countingDownloader) DownloadThumbnail(ctx context.Context, name string) (io.ReadCloser, error) {
	return c.m.DownloadThumbnail(ctx, name)
}
func (c *countingDownloader) WatchState(ctx context.Context, fn func(*printer.State)) error {
	return c.m.WatchState(ctx, fn)
}

// Заголовок G-кода молчит (как у OrcaSlicer 2.5.1): время — из имени файла,
// граммы — из length (мм) из WS-списка.
func TestListFilesFallbacks(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "orcs_3_PLA_2h30m.gcode", Size: 1000, FilamentMM: 3048},
			{Path: "/p", Name: "Holder.gcode", Size: 2000, FilamentMM: 0},
		},
		gcode: map[string][]byte{
			// В заголовке только плотность — времени и грамм нет.
			"orcs_3_PLA_2h30m.gcode": []byte("; filament_density: 1.24\n"),
			"Holder.gcode":           []byte("G28\nG1 X10\n"),
		},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if meta[0].PrintTime == nil || *meta[0].PrintTime != 2*3600+30*60 {
		t.Errorf("orcs: PrintTime = %v, want 9000 (из имени)", meta[0].PrintTime)
	}
	if meta[0].Filament == nil {
		t.Fatal("orcs: Filament = nil, want пересчёт из 3048 мм")
	}
	// 3048 мм × π·0.875²/1000 × 1.24 ≈ 9.09 г
	if *meta[0].Filament < 9.0 || *meta[0].Filament > 9.2 {
		t.Errorf("orcs: Filament = %v, want ≈ 9.09", *meta[0].Filament)
	}
	if meta[1].PrintTime != nil || meta[1].Filament != nil {
		t.Errorf("Holder: ожидалось «—», got PrintTime=%v Filament=%v", meta[1].PrintTime, meta[1].Filament)
	}
}

// Заголовок G-кода приоритетнее имени: имя «1h0m0s», в файле 2 часа.
func TestListFilesHeaderBeatsFilename(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "part_PLA_1h0m0s.gcode", Size: 100, FilamentMM: 1000},
		},
		gcode: map[string][]byte{
			"part_PLA_1h0m0s.gcode": []byte("; estimated printing time (normal mode) = 2h 0m 0s\n"),
		},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if meta[0].PrintTime == nil || *meta[0].PrintTime != 7200 {
		t.Errorf("PrintTime = %v, want 7200 (из заголовка, не из имени)", meta[0].PrintTime)
	}
}

// Превью: data URI из скачанного PNG; при отсутствии — пустая строка.
func TestListFilesThumb(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
		gcode:  map[string][]byte{"a.gcode": []byte("G28\n"), "b.gcode": []byte("G28\n")},
		thumbs: map[string][]byte{"a.gcode": png},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if !strings.HasPrefix(meta[0].Thumb, "data:image/png;base64,") {
		t.Errorf("a: Thumb = %q, want data URI", meta[0].Thumb)
	}
	if meta[1].Thumb != "" {
		t.Errorf("b: Thumb = %q, want «» (нет превью)", meta[1].Thumb)
	}
}
