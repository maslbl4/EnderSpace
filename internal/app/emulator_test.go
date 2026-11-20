package app

import (
	"context"
	"testing"
	"time"

	"ender-space/internal/printer"
	"ender-space/internal/printer/emulator"
)

// Полный поток App против in-memory эмулятора: Connect → список и
// обогащение → прошивка в Status → печать (запуск + подтверждение +
// завершение) → удаление. «Дымовой» чек, что слой приложения и драйвер
// говорят на одном языке — без сети, быстро.
func TestAppAgainstEmulator(t *testing.T) {
	fastRetries(t)
	fastDeletes(t)
	rec := newEventRecorder()
	a := NewAppWithClientAndSink(t.TempDir(),
		func(ip string) printer.Printer {
			return emulator.New(emulator.WithPrintDuration(300*time.Millisecond),
				emulator.WithTick(10*time.Millisecond))
		}, rec.sink)
	ctx := context.Background()
	// IP для эмулятора любой: принтера по нему нет и не нужно.
	if err := a.Connect(ctx, "192.168.99.1"); err != nil {
		t.Fatalf("Connect к эмулятору: %v", err)
	}
	t.Cleanup(func() { _ = a.Disconnect() })

	// Модель и прошивка — из драйвера.
	waitStatus(t, a, func(st printer.PrinterStatus) bool { return st.FirmwareVersion != "" })
	if st := a.Status(); st.Model != "F005" {
		t.Errorf("Model = %q, want F005", st.Model)
	}

	// Список и обогащение: у файлов с заголовком время доходит до UI.
	meta, err := a.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(meta) != 8 {
		t.Fatalf("файлов = %d, want 8", len(meta))
	}
	rec.waitForDone(t)
	rec.mu.Lock()
	var withTime int
	for _, fm := range rec.enriched {
		if fm.PrintTime != nil {
			withTime++
		}
	}
	rec.mu.Unlock()
	if withTime == 0 {
		t.Error("после обогащения ни у одного файла нет времени печати")
	}

	// Печать: запуск с подтверждением, затем самостоятельное «довпечатывание».
	const full = "/usr/data/printer_data/gcodes/benchy_PLA_1h15m.gcode"
	if err := a.StartPrint(ctx, full); err != nil {
		t.Fatalf("StartPrint: %v", err)
	}
	waitStatus(t, a, func(st printer.PrinterStatus) bool {
		return st.Printing && st.PrintFile == full
	})
	waitStatus(t, a, func(st printer.PrinterStatus) bool { return !st.Printing })

	// Удаление: файл исчезает из списка.
	if err := a.DeleteFiles(ctx, []string{full}); err != nil {
		t.Fatalf("DeleteFiles: %v", err)
	}
	files, err := a.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles после удаления: %v", err)
	}
	if len(files) != 7 {
		t.Fatalf("файлов после удаления = %d, want 7", len(files))
	}
	for _, f := range files {
		if f.Name == "benchy_PLA_1h15m.gcode" {
			t.Fatal("удалённый файл остался в списке")
		}
	}
}
