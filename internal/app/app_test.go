package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

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
	flaky       map[string]int    // name → остаток гарантированных сбоев (G-код)
	thumbFlaky  map[string]int    // name → остаток гарантированных сбоев (превью)
	deleteErr   map[string]error  // "path/name" → ошибка удаления
	listLag     int               // файл исчезает из списка через N вызовов ListFiles (0 — сразу)
	pendingDel  map[string]int    // "path|name" → остаток вызовов ListFiles до исчезновения
	deleted     []string
	printed     []string    // "path/name" → ушедшие printprt
	statePush   chan string // сырой JSON состояния; WatchState пушит в наблюдателя
}

func (m *mockPrinter) Info(ctx context.Context) (printer.Info, error) {
	if !m.infoOK {
		return printer.Info{}, errors.New("mock: принтер не отвечает")
	}
	return printer.Info{MAC: "TEST", Model: "F005"}, nil
}

func (m *mockPrinter) ListFiles(ctx context.Context) ([]printer.RawFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Принтер обрабатывает deleteprt асинхронно: файл исчезает из списка
	// через listLag вызовов (pendingDel), а не мгновенно.
	for key, left := range m.pendingDel {
		if left <= 1 {
			m.removeFile(key)
			delete(m.pendingDel, key)
		} else {
			m.pendingDel[key] = left - 1
		}
	}
	out := make([]printer.RawFile, len(m.files))
	copy(out, m.files)
	return out, nil
}

func (m *mockPrinter) DeleteFile(ctx context.Context, path, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := path + "/" + name
	if err := m.deleteErr[key]; err != nil {
		return err
	}
	m.deleted = append(m.deleted, key)
	pathName := path + "|" + name
	if m.listLag == 0 {
		m.removeFile(pathName)
		return nil
	}
	// Повторный deleteprt не ускоряет принтер: отсчёт идёт от первой операции.
	if m.pendingDel == nil {
		m.pendingDel = map[string]int{}
	}
	if _, ok := m.pendingDel[pathName]; !ok {
		m.pendingDel[pathName] = m.listLag
	}
	return nil
}

func (m *mockPrinter) PrintFile(ctx context.Context, path, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.printed = append(m.printed, path+"/"+name)
	return nil
}

// printedCount / printedList — чтение m.printed под мьютексом.
func (m *mockPrinter) printedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.printed)
}

func (m *mockPrinter) printedList() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.printed))
	copy(out, m.printed)
	return out
}

// removeFile — исключает «path|name» из m.files (вызывать под m.mu).
func (m *mockPrinter) removeFile(pathName string) {
	out := m.files[:0]
	for _, f := range m.files {
		if f.Path+"|"+f.Name != pathName {
			out = append(out, f)
		}
	}
	m.files = out
}

func (m *mockPrinter) DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.downloadErr[name]; err != nil {
		return nil, err
	}
	if n := m.flaky[name]; n > 0 {
		m.flaky[name]--
		return nil, errors.New("mock: транзиентный сбой")
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
	if n := m.thumbFlaky[name]; n > 0 {
		m.thumbFlaky[name]--
		return nil, errors.New("mock: транзиентный сбой превью")
	}
	body, ok := m.thumbs[name]
	if !ok {
		// «Превью точно нет» — окончательный 404, не сетевой сбой.
		return nil, printer.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// WatchState: без statePush молчит (большинство тестов состояние не
// проверяют); с каналом — пушит туда присланный JSON как push-сообщения.
func (m *mockPrinter) WatchState(ctx context.Context, fn func(*printer.State)) error {
	if m.statePush == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	st := printer.NewState()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case raw := <-m.statePush:
			if err := st.Merge([]byte(raw)); err == nil {
				fn(st)
			}
		}
	}
}

func newTestApp(t *testing.T, m *mockPrinter) *App {
	t.Helper()
	return NewAppWithClient(t.TempDir(), func(ip string) PrinterAPI { return m })
}

// eventRecorder — EventSink для тестов: собирает события обогащения.
type eventRecorder struct {
	mu       sync.Mutex
	enriched []printer.FileMeta
	progress []EnrichProgress
	done     chan struct{} // закрыт после files:done (однократно)
}

func newEventRecorder() *eventRecorder {
	return &eventRecorder{done: make(chan struct{})}
}

func (r *eventRecorder) sink(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case filesEnrichedEvent:
		r.enriched = append(r.enriched, data.(printer.FileMeta))
	case filesProgressEvent:
		r.progress = append(r.progress, data.(EnrichProgress))
	case filesDoneEvent:
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
}

// waitForDone — ждёт, пока фоновое обогащение не шлёт files:done.
func (r *eventRecorder) waitForDone(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("files:done не пришёл за 10 с")
	}
}

// reset — готовит коллектор к следующему проходу (⟳).
func (r *eventRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enriched = nil
	r.progress = nil
	r.done = make(chan struct{})
}

// fastRetries — ретрай-паузы на миллисекунды (иначе тесты ждут 3.5 с на
// каждый провалившийся файл).
func fastRetries(t *testing.T) {
	t.Helper()
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}
	t.Cleanup(func() { retryDelays = old })
}

// enrichedByName — данные файла из события files:enriched.
func (r *eventRecorder) enrichedByName(t *testing.T, name string) printer.FileMeta {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, fm := range r.enriched {
		if fm.Name == name {
			return fm
		}
	}
	t.Fatalf("files:enriched для %s не пришёл", name)
	return printer.FileMeta{}
}

// newTestAppWithEvents — App с sink-коллектором и уже «подключённым» клиентом.
func newTestAppWithEvents(t *testing.T, m *mockPrinter) (*App, *eventRecorder) {
	t.Helper()
	rec := newEventRecorder()
	a := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	a.connected = true
	a.client = m
	t.Cleanup(func() { _ = a.Disconnect() })
	return a, rec
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
	app, rec := newTestAppWithEvents(t, m)

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("фаза 1: len = %d, want 2 (список возвращается сразу)", len(meta))
	}
	rec.waitForDone(t)
	a := rec.enrichedByName(t, "a.gcode")
	if a.PrintTime == nil || *a.PrintTime != 3600 {
		t.Errorf("a: PrintTime = %v, want 3600", a.PrintTime)
	}
	if a.Filament == nil || *a.Filament != 10 {
		t.Errorf("a: Filament = %v, want 10", a.Filament)
	}
	b := rec.enrichedByName(t, "b.gcode")
	if b.PrintTime == nil || *b.PrintTime != 7200 {
		t.Errorf("b: PrintTime = %v, want 7200", b.PrintTime)
	}
	if b.Filament == nil || *b.Filament != 20 {
		t.Errorf("b: Filament = %v, want 20", b.Filament)
	}
}

func TestListFilesParserFailureKeepsFile(t *testing.T) {
	fastRetries(t)
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
		gcode:       map[string][]byte{"a.gcode": []byte("; total filament used [g] = 5\n")},
		downloadErr: map[string]error{"b.gcode": errors.New("mock: сеть упала")},
	}
	app, rec := newTestAppWithEvents(t, m)

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ошибка скачивания одного файла не должна ронять список: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("фаза 1: len = %d, want 2", len(meta))
	}
	rec.waitForDone(t)
	b := rec.enrichedByName(t, "b.gcode")
	if b.PrintTime != nil || b.Filament != nil {
		t.Errorf("b: ожидалось «—» (nil), got PrintTime=%v Filament=%v", b.PrintTime, b.Filament)
	}
	a := rec.enrichedByName(t, "a.gcode")
	if a.Filament == nil || *a.Filament != 5 {
		t.Errorf("a: Filament = %v, want 5", a.Filament)
	}
}

// fastDeletes — сокращает паузу сверки после deleteprt (1.5 c) в тестах.
func fastDeletes(t *testing.T) {
	t.Helper()
	old := deleteCheckDelay
	deleteCheckDelay = 5 * time.Millisecond
	t.Cleanup(func() { deleteCheckDelay = old })
}

func TestDeleteFilesAllOK(t *testing.T) {
	fastDeletes(t)
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	if err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode"}); err != nil {
		t.Fatalf("DeleteFiles: %v", err)
	}
	if len(m.deleted) != 1 || m.deleted[0] != "/p/a.gcode" {
		t.Errorf("deleted = %v", m.deleted)
	}
	if len(m.files) != 0 {
		t.Errorf("файл должен исчезнуть из списка, осталось %d", len(m.files))
	}
}

// Принтер не шлёт ack на deleteprt и обрабатывает его асинхронно: файл
// исчезает из списка только на 2-й проверке. DeleteFiles должен дождаться
// и повторной отправкой — и завершить успехом.
func TestDeleteFilesDelayedProcessing(t *testing.T) {
	fastDeletes(t)
	m := &mockPrinter{
		files:   []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		listLag: 2, // исчезнет после 2-го ListFiles
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	if err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode"}); err != nil {
		t.Fatalf("DeleteFiles: %v (принтер обработал, просто с задержкой)", err)
	}
	if len(m.files) != 0 {
		t.Errorf("файл должен исчезнуть из списка, осталось %d", len(m.files))
	}
	if len(m.deleted) < 1 {
		t.Errorf("deleteprt должен был уйти, deleted = %v", m.deleted)
	}
}

// fastPrintConfirm — сокращает ожидание подтверждения запуска печати в тестах.
func fastPrintConfirm(t *testing.T) {
	t.Helper()
	oldTO, oldPoll := printConfirmTimeout, printPoll
	printConfirmTimeout = 500 * time.Millisecond
	printPoll = 10 * time.Millisecond
	t.Cleanup(func() { printConfirmTimeout, printPoll = oldTO, oldPoll })
}

// waitStatus — ждёт, пока состояние из statePush не дойдёт до App.Status.
func waitStatus(t *testing.T, app *App, want func(printer.PrinterStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if want(app.Status()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Status не достигло нужного состояния: %v", app.Status())
}

// StartPrint: принтер «запустил» печать (state=1, printFileName совпал) —
// успех, printprt ушёл ровно один раз.
func TestStartPrintHappyPath(t *testing.T) {
	fastPrintConfirm(t)
	m := &mockPrinter{infoOK: true, statePush: make(chan string, 8)}
	app := newTestApp(t, m)
	if err := app.Connect(context.Background(), "192.168.3.166"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Disconnect() })

	const full = "/p/a.gcode"
	// Принтер подтверждает запуск пушем состояния после printprt.
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if m.printedCount() > 0 {
				m.statePush <- `{"state":1,"printFileName":"/p/a.gcode"}`
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	if err := app.StartPrint(context.Background(), full); err != nil {
		t.Fatalf("StartPrint: %v", err)
	}
	if got := m.printedList(); len(got) != 1 || got[0] != full {
		t.Errorf("printed = %v, want [%s]", got, full)
	}
}

// Принтер уже печатает другой файл — StartPrint отказывается, printprt
// не отправляется.
func TestStartPrintRejectsWhilePrinting(t *testing.T) {
	fastPrintConfirm(t)
	m := &mockPrinter{infoOK: true, statePush: make(chan string, 8)}
	app := newTestApp(t, m)
	if err := app.Connect(context.Background(), "192.168.3.166"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Disconnect() })

	m.statePush <- `{"state":1,"printFileName":"/p/other.gcode"}`
	waitStatus(t, app, func(st printer.PrinterStatus) bool { return st.Printing })
	if st := app.Status(); st.PrintFile != "/p/other.gcode" {
		t.Errorf("Status().PrintFile = %q, want /p/other.gcode", st.PrintFile)
	}

	if err := app.StartPrint(context.Background(), "/p/a.gcode"); err == nil {
		t.Fatal("ожидалась ошибка: принтер уже печатает")
	}
	if got := m.printedList(); len(got) != 0 {
		t.Errorf("printprt не должен был уйти, printed = %v", got)
	}
}

// printprt ушёл, но состояние не меняется — честный таймаут подтверждения.
func TestStartPrintNoConfirmTimeout(t *testing.T) {
	fastPrintConfirm(t)
	m := &mockPrinter{infoOK: true, statePush: make(chan string, 8)}
	app := newTestApp(t, m)
	if err := app.Connect(context.Background(), "192.168.3.166"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Disconnect() })

	start := time.Now()
	if err := app.StartPrint(context.Background(), "/p/a.gcode"); err == nil {
		t.Fatal("ожидалась ошибка: печать не подтвердилась")
	} else if time.Since(start) < 400*time.Millisecond {
		t.Errorf("стало ошибкой за %s — подтверждать не ждали", time.Since(start))
	}
	if got := m.printedList(); len(got) != 1 {
		t.Errorf("printprt был отправлен, printed = %v", got)
	}
}

// Файл, который сейчас печатается, удалить нельзя: deleteprt не уходит.
func TestDeleteFilesRejectsPrintingFile(t *testing.T) {
	fastDeletes(t)
	m := &mockPrinter{
		infoOK:    true,
		statePush: make(chan string, 8),
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
	}
	app := newTestApp(t, m)
	if err := app.Connect(context.Background(), "192.168.3.166"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Disconnect() })

	m.statePush <- `{"state":1,"printFileName":"/p/a.gcode"}`
	waitStatus(t, app, func(st printer.PrinterStatus) bool { return st.Printing })

	if err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode"}); err == nil {
		t.Fatal("ожидалась ошибка: файл сейчас печатается")
	}
	m.mu.Lock()
	deleted := len(m.deleted)
	m.mu.Unlock()
	if deleted != 0 {
		t.Errorf("deleteprt не должен был уйти, удалено %d", deleted)
	}
}

// b.gcode принтер «не слышит» (WS-отказ) и файл остаётся в списке:
// после всех раундов — честный отчёт «deleted 1 of 2».
func TestDeleteFilesStillPresent(t *testing.T) {
	fastDeletes(t)
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
		},
		deleteErr: map[string]error{"/p/b.gcode": errors.New("mock: отказ")},
	}
	app := newTestApp(t, m)
	app.connected = true
	app.client = m

	err := app.DeleteFiles(context.Background(), []string{"/p/a.gcode", "/p/b.gcode"})
	if err == nil {
		t.Fatal("ожидалась ошибка (b.gcode остался на принтере)")
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("отчёт об ошибке = %q, want упоминание «1 of 2»", err.Error())
	}
	if len(m.deleted) != 1 || m.deleted[0] != "/p/a.gcode" {
		t.Errorf("deleted = %v, want [/p/a.gcode]", m.deleted)
	}
	if len(m.files) != 1 || m.files[0].Name != "b.gcode" {
		t.Errorf("в списке должен остаться только b.gcode, got %v", m.files)
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
	if st.System != config.DefaultSystemBytes {
		t.Errorf("System = %d, want дефолт %d", st.System, config.DefaultSystemBytes)
	}
	if st.Free != st.Total-300-st.System {
		t.Errorf("Free = %d, want Total-Used-System = %d", st.Free, st.Total-300-st.System)
	}
}

func TestListFilesCachesGcodeInfo(t *testing.T) {
	downloads := 0
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode: map[string][]byte{"a.gcode": []byte("; total filament used [g] = 5\n")},
	}
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	app.connected = true
	t.Cleanup(func() { _ = app.Disconnect() })
	// Подсчёт скачиваний обёрткой
	wrapped := &countingDownloader{m: m, n: &downloads}
	app.client = wrapped

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)

	// Вторая фаза 1: данные уже в кэше — время/граммы сразу, без ожидания
	// событий и без повторного скачивания.
	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 {
		t.Fatalf("фаза 1: len = %d, want 1", len(meta))
	}
	if meta[0].Filament == nil || *meta[0].Filament != 5 {
		t.Errorf("фаза 1: Filament = %v, want 5 (из кэша сразу)", meta[0].Filament)
	}
	rec.waitForDone(t)
	if downloads != 1 {
		t.Errorf("скачиваний = %d, want 1 (кэш по path+size)", downloads)
	}
}

// countingDownloader — считает обращения DownloadGcode/DownloadThumbnail.
type countingDownloader struct {
	m      *mockPrinter
	n      *int // DownloadGcode; nil — не считаем
	nThumb *int // DownloadThumbnail; nil — не считаем
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
func (c *countingDownloader) PrintFile(ctx context.Context, path, name string) error {
	return c.m.PrintFile(ctx, path, name)
}
func (c *countingDownloader) DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error) {
	if c.n != nil {
		*c.n += 1
	}
	return c.m.DownloadGcode(ctx, name)
}
func (c *countingDownloader) DownloadThumbnail(ctx context.Context, name string) (io.ReadCloser, error) {
	if c.nThumb != nil {
		*c.nThumb += 1
	}
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
	app, rec := newTestAppWithEvents(t, m)

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("фаза 1: len = %d, want 2", len(meta))
	}
	rec.waitForDone(t)
	orcs := rec.enrichedByName(t, "orcs_3_PLA_2h30m.gcode")
	if orcs.PrintTime == nil || *orcs.PrintTime != 2*3600+30*60 {
		t.Errorf("orcs: PrintTime = %v, want 9000 (из имени)", orcs.PrintTime)
	}
	if orcs.Filament == nil {
		t.Fatal("orcs: Filament = nil, want пересчёт из 3048 мм")
	}
	// 3048 мм × π·0.875²/1000 × 1.24 ≈ 9.09 г
	if *orcs.Filament < 9.0 || *orcs.Filament > 9.2 {
		t.Errorf("orcs: Filament = %v, want ≈ 9.09", *orcs.Filament)
	}
	holder := rec.enrichedByName(t, "Holder.gcode")
	if holder.PrintTime != nil || holder.Filament != nil {
		t.Errorf("Holder: ожидалось «—», got PrintTime=%v Filament=%v", holder.PrintTime, holder.Filament)
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
	app, rec := newTestAppWithEvents(t, m)

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	rec.waitForDone(t)
	fm := rec.enrichedByName(t, "part_PLA_1h0m0s.gcode")
	if fm.PrintTime == nil || *fm.PrintTime != 7200 {
		t.Errorf("PrintTime = %v, want 7200 (из заголовка, не из имени)", fm.PrintTime)
	}
}

// События обогащения: на каждый файл files:enriched + files:progress
// (Done растёт, Total постоянен), в конце files:done; фаза 1 — сразу.
func TestEnrichEmitsProgress(t *testing.T) {
	m := &mockPrinter{
		files: []printer.RawFile{
			{Path: "/p", Name: "a.gcode", Size: 100},
			{Path: "/p", Name: "b.gcode", Size: 200},
			{Path: "/p", Name: "c.gcode", Size: 300},
		},
		gcode: map[string][]byte{
			"a.gcode": []byte("G28\n"),
			"b.gcode": []byte("G28\n"),
			"c.gcode": []byte("G28\n"),
		},
	}
	app, rec := newTestAppWithEvents(t, m)

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 3 {
		t.Fatalf("фаза 1: len = %d, want 3 (список сразу)", len(meta))
	}
	rec.waitForDone(t)

	rec.mu.Lock()
	progs := append([]EnrichProgress(nil), rec.progress...)
	enriched := len(rec.enriched)
	rec.mu.Unlock()
	if enriched != 3 {
		t.Errorf("files:enriched = %d, want 3", enriched)
	}
	if len(progs) != 3 {
		t.Fatalf("files:progress = %d, want 3 (по одному на файл)", len(progs))
	}
	for _, p := range progs {
		if p.Total != 3 {
			t.Errorf("Total = %d, want 3", p.Total)
		}
	}
	if progs[len(progs)-1].Done != 3 {
		t.Errorf("последний Done = %d, want 3", progs[len(progs)-1].Done)
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
	app, rec := newTestAppWithEvents(t, m)

	meta, err := app.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 2 {
		t.Fatalf("фаза 1: len = %d, want 2", len(meta))
	}
	rec.waitForDone(t)
	a := rec.enrichedByName(t, "a.gcode")
	if !strings.HasPrefix(a.Thumb, "data:image/png;base64,") {
		t.Errorf("a: Thumb = %q, want data URI", a.Thumb)
	}
	b := rec.enrichedByName(t, "b.gcode")
	if b.Thumb != "" {
		t.Errorf("b: Thumb = %q, want «» (нет превью)", b.Thumb)
	}
}

// Транзиентный сбой: 2 первых попытки упали, третья успешна — ретраи
// сработали, данные на месте.
func TestEnrichRetriesTransientFailure(t *testing.T) {
	fastRetries(t)
	downloads := 0
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode: map[string][]byte{"a.gcode": []byte("; total filament used [g] = 7\n")},
		flaky: map[string]int{"a.gcode": 2},
	}
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	app.connected = true
	app.client = &countingDownloader{m: m, n: &downloads}
	t.Cleanup(func() { _ = app.Disconnect() })

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	fm := rec.enrichedByName(t, "a.gcode")
	if fm.Filament == nil || *fm.Filament != 7 {
		t.Errorf("Filament = %v, want 7 (третья попытка успешна)", fm.Filament)
	}
	if downloads != 3 {
		t.Errorf("скачиваний = %d, want 3 (2 сбоя + успех)", downloads)
	}
}

// Провал не кэшируется: следующий проход (⟳) запрашивает файл снова и
// получает данные, когда принтер «выздоровел».
func TestFailureNotCachedRetriesNextPass(t *testing.T) {
	fastRetries(t)
	downloads := 0
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode: map[string][]byte{"a.gcode": []byte("; total filament used [g] = 7\n")},
		flaky: map[string]int{"a.gcode": 100}, // «всегда» падают
	}
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	app.connected = true
	app.client = &countingDownloader{m: m, n: &downloads}
	t.Cleanup(func() { _ = app.Disconnect() })

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	first := downloads
	if first != 4 {
		t.Fatalf("скачиваний после первого прохода = %d, want 4 (1 + 3 ретрая)", first)
	}
	// Принтер «выздоровел»: сбои убраны.
	m.mu.Lock()
	m.flaky = map[string]int{}
	m.mu.Unlock()

	rec.reset()
	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	fm := rec.enrichedByName(t, "a.gcode")
	if fm.Filament == nil || *fm.Filament != 7 {
		t.Errorf("Filament = %v, want 7 (повтор после провала успешен)", fm.Filament)
	}
	if downloads != first+1 {
		t.Errorf("скачиваний = %d, want %d (провал не закэширован)", downloads, first+1)
	}
}

// Сетевой провал превью не кэшируется: следующий проход (⟳) запрашивает
// снова и получает data URI, когда принтер «выздоровел».
func TestThumbFailureNotCached(t *testing.T) {
	fastRetries(t)
	png := []byte{0x89, 'P', 'N', 'G'}
	m := &mockPrinter{
		files:      []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode:      map[string][]byte{"a.gcode": []byte("G28\n")},
		thumbFlaky: map[string]int{"a.gcode": 100}, // «всегда» падают
	}
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	app.connected = true
	app.client = m
	t.Cleanup(func() { _ = app.Disconnect() })

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	if fm := rec.enrichedByName(t, "a.gcode"); fm.Thumb != "" {
		t.Fatalf("первый проход: Thumb = %q, want «» (превью не скачано)", fm.Thumb)
	}
	// Принтер «выздоровел»: сбои убраны, превью появилось.
	m.mu.Lock()
	m.thumbFlaky = map[string]int{}
	m.thumbs = map[string][]byte{"a.gcode": png}
	m.mu.Unlock()

	rec.reset()
	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	fm := rec.enrichedByName(t, "a.gcode")
	if !strings.HasPrefix(fm.Thumb, "data:image/png;base64,") {
		t.Errorf("второй проход: Thumb = %q, want data URI (провал не закэширован)", fm.Thumb)
	}
}

// 404 превью — окончательный ответ: не ретраится (одна попытка) и кэшируется
// — следующий проход превью не запрашивает.
func TestThumbNotFoundIsFinal(t *testing.T) {
	fastRetries(t)
	png := []byte{0x89, 'P', 'N', 'G'}
	m := &mockPrinter{
		files: []printer.RawFile{{Path: "/p", Name: "a.gcode", Size: 100}},
		gcode: map[string][]byte{"a.gcode": []byte("G28\n")},
	}
	cdn := &countingDownloader{m: m, nThumb: new(int)}
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(), func(ip string) PrinterAPI { return m }, rec.sink)
	app.connected = true
	app.client = cdn
	t.Cleanup(func() { _ = app.Disconnect() })

	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	if *cdn.nThumb != 1 {
		t.Errorf("попыток превью = %d, want 1 (404 не ретраится)", *cdn.nThumb)
	}
	// «Превью появилось» — но 404 закэширован: повторных запросов нет.
	m.mu.Lock()
	m.thumbs = map[string][]byte{"a.gcode": png}
	m.mu.Unlock()
	rec.reset()
	if _, err := app.ListFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.waitForDone(t)
	if *cdn.nThumb != 1 {
		t.Errorf("попыток превью = %d, want 1 (404 закэширован)", *cdn.nThumb)
	}
}
