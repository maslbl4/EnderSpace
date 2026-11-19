package app

// Живые проверки против реального принтера (192.168.3.166, Ender-3 V3 KE).
// Запускаются только с ENDERSPACE_LIVE=1, в обычном прогоне пропускаются:
//
//	$env:ENDERSPACE_LIVE=1; go test ./internal/app/ -run TestLive -v -count=1

import (
	"context"
	"os"
	"testing"
	"time"

	"ender-space/internal/printer"
)

// Полный цикл: Connect → фаза 1 (мгновенный список) → фаза 2 (фоновое
// обогащение до files:done). После done у каждого файла должно быть время
// печати и/или масса (из заголовка, имени или length WS-списка). Превью
// считаем и логгируем, но не трим: у файла может genuinely не быть PNG.
func TestLiveEnrichAllFiles(t *testing.T) {
	if os.Getenv("ENDERSPACE_LIVE") != "1" {
		t.Skip("live-тест: задайте ENDERSPACE_LIVE=1")
	}
	const ip = "192.168.3.166"
	rec := newEventRecorder()
	app := NewAppWithClientAndSink(t.TempDir(),
		func(s string) PrinterAPI { return printer.New("http://" + s) }, rec.sink)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := app.Connect(ctx, ip); err != nil {
		t.Fatalf("Connect %s: %v", ip, err)
	}
	t.Cleanup(func() { _ = app.Disconnect() })

	start := time.Now()
	meta, err := app.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	t.Logf("фаза 1: %d файлов за %s", len(meta), time.Since(start).Round(time.Millisecond))
	if len(meta) == 0 {
		t.Fatal("список файлов пуст")
	}

	select {
	case <-rec.done:
	case <-time.After(2 * time.Minute):
		t.Fatal("files:done не пришёл за 2 минуты")
	}
	elapsed := time.Since(start).Round(time.Second)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.enriched) != len(meta) {
		t.Errorf("files:enriched = %d, want %d", len(rec.enriched), len(meta))
	}
	var noData, noThumb []string
	for _, fm := range rec.enriched {
		if fm.PrintTime == nil && fm.Filament == nil {
			noData = append(noData, fm.Name)
		}
		if fm.Thumb == "" {
			noThumb = append(noThumb, fm.Name)
		}
	}
	t.Logf("обогащение: %d файлов за %s; без данных %d, без превью %d",
		len(meta), elapsed, len(noData), len(noThumb))
	for _, n := range noThumb {
		t.Logf("  без превью: %s", n)
	}
	if len(noData) > 0 {
		t.Errorf("%d файлов без времени и массы: %v", len(noData), noData)
	}
}
