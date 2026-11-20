package emulator

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"ender-space/internal/printer"
)

func TestImplementsPrinterInterface(t *testing.T) {
	var _ printer.Printer = New()
}

func TestInfoAndStatus(t *testing.T) {
	e := New()
	info, err := e.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	// Состояние сразу: не печатает, прошивка на месте.
	ds := e.status()
	if ds.Printing || ds.PrintFile != "" {
		t.Errorf("стартовое состояние = %+v, want не печатает", ds)
	}
	if ds.Firmware != "V1.1.0.15" {
		t.Errorf("Firmware = %q, want V1.1.0.15", ds.Firmware)
	}
}

func TestListFilesAndDelete(t *testing.T) {
	e := New()
	ctx := context.Background()
	files, err := e.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 8 {
		t.Fatalf("ListFiles = %d файлов, want 8", len(files))
	}

	target := files[0]
	if err := e.DeleteFile(ctx, target.Path, target.Name); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	files, _ = e.ListFiles(ctx)
	if len(files) != 7 {
		t.Fatalf("после удаления = %d файлов, want 7", len(files))
	}
	for _, f := range files {
		if f.Name == target.Name {
			t.Fatalf("%s должен исчезнуть из списка", target.Name)
		}
	}
	// Удалённый файл не качается (404).
	if _, err := e.DownloadGcode(ctx, target.Path, target.Name); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadGcode после удаления: err = %v, want 404", err)
	}
	// Несуществующий файл — ошибка.
	if err := e.DeleteFile(ctx, "/p", "nope.gcode"); err == nil {
		t.Error("DeleteFile для несуществующего файла: ожидалась ошибка")
	}
}

func TestDownloads(t *testing.T) {
	e := New()
	ctx := context.Background()
	const dir = "/usr/data/printer_data/gcodes"

	// G-код: заголовок, который парсер обогащения понимает (время + мм).
	rc, err := e.DownloadGcode(ctx, dir, "benchy_PLA_1h15m.gcode")
	if err != nil {
		t.Fatalf("DownloadGcode: %v", err)
	}
	body, _ := io.ReadAll(rc)
	rc.Close()
	if !strings.Contains(string(body), "estimated printing time") ||
		!strings.Contains(string(body), "filament used [mm]") {
		t.Errorf("синтетический G-код без заголовка: %q", body)
	}
	if _, err := e.DownloadGcode(ctx, dir, "nope.gcode"); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadGcode для неизвестного файла: err = %v, want 404", err)
	}

	// Превью: у большинства есть, у nothumb — 404.
	rc, err = e.DownloadThumbnail(ctx, dir, "vase_spiralize.gcode")
	if err != nil {
		t.Fatalf("DownloadThumbnail: %v", err)
	}
	png, _ := io.ReadAll(rc)
	rc.Close()
	if len(png) == 0 || png[0] != 0x89 {
		t.Error("пустое или не-PNG превью")
	}
	if _, err := e.DownloadThumbnail(ctx, dir, "nothumb_raw_dump.gcode"); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadThumbnail nothumb: err = %v, want 404", err)
	}
}

// waitForStatus — ждёт статус, удовлетворяющий cond (с дедлайном).
func waitForStatus(t *testing.T, ch <-chan printer.DeviceStatus, cond func(printer.DeviceStatus) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ds := <-ch:
			if cond(ds) {
				return
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	t.Fatal("нужное состояние не пришло за 3 с")
}

// Полный цикл печати: PrintFile → статус «печатает» с полным путём →
// самостоятельное «довпечатывание» → статус «не печатает».
func TestPrintCycle(t *testing.T) {
	e := New(WithPrintDuration(50*time.Millisecond), WithTick(5*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	statuses := make(chan printer.DeviceStatus, 64)
	done := make(chan error, 1)
	go func() {
		done <- e.WatchStatus(ctx, func(ds printer.DeviceStatus) { statuses <- ds })
	}()

	const full = "/usr/data/printer_data/gcodes/benchy_PLA_1h15m.gcode"
	if err := e.PrintFile(context.Background(), "/usr/data/printer_data/gcodes", "benchy_PLA_1h15m.gcode"); err != nil {
		t.Fatalf("PrintFile: %v", err)
	}
	waitForStatus(t, statuses, func(ds printer.DeviceStatus) bool {
		return ds.Printing && ds.PrintFile == full
	})
	// «Допечатывает» сам — статус возвращается в простой.
	waitForStatus(t, statuses, func(ds printer.DeviceStatus) bool { return !ds.Printing })

	// WatchStatus корректно завершается по отмене ctx.
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) &&
			!strings.Contains(err.Error(), "context canceled") {
			t.Errorf("WatchStatus после cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchStatus не завершилась после cancel")
	}
}

// Одновременно печатать два файла нельзя (как и на реальном принтере).
func TestPrintRejectsSecondStart(t *testing.T) {
	e := New()
	ctx := context.Background()
	if err := e.PrintFile(ctx, "/p", "a.gcode"); err != nil {
		t.Fatalf("первая печать: %v", err)
	}
	if err := e.PrintFile(ctx, "/p", "b.gcode"); err == nil {
		t.Fatal("вторая печать во время первой: ожидалась ошибка")
	}
}
