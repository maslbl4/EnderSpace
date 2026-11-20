package nebulaos

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ender-space/internal/printer"

	"github.com/gorilla/websocket"
)

// Статическая проверка: Client реализует printer.Printer.
var _ printer.Printer = (*Client)(nil)

// --- Тестовый Moonraker: HTTP + WS (JSON-RPC) на одном httptest-сервере. ---

type testMoonraker struct {
	srv          *httptest.Server
	mu           sync.Mutex
	printStarted []string // filename из /printer/print/start
	rpcMethods   []string // методы JSON-RPC, пришедшие по WS
	deletePaths  []string // path-параметр server.files.delete_file
	// статусы, которые WS-сервер должен выслать после снапшота
	// (каждый — отдельным notify_status_update; nil — не шлёт).
	statusPush [][]byte
	// deleteErr — WS-сервер отвечает на delete_file ошибкой.
	deleteErr string
	// nebulaosInfo404 — /machine/nebulaos/info отвечает 404 (тест фолбэка).
	nebulaosInfo404 bool
}

func newTestMoonraker(t *testing.T) *testMoonraker {
	t.Helper()
	mk := &testMoonraker{}
	up := websocket.Upgrader{}
	mux := http.NewServeMux()

	mux.HandleFunc("/server/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hostname":"nebulaos-32f7","klipper_version":"v0.13.1","version":"v0.9.3"}`))
	})
	mux.HandleFunc("/machine/nebulaos/info", func(w http.ResponseWriter, r *http.Request) {
		if mk.nebulaosInfo404 {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"build":{"available":true,"nebulaos_version":"1.0.2","build_date":"2026-11-09"},"slot":"a"}`))
	})
	mux.HandleFunc("/server/files/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("root") != "gcodes" {
			http.Error(w, "root", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[
			{"path":"a.gcode","modified":1794988291.5,"size":123,"permissions":"rw"},
			{"path":"sub/b.gcode","modified":1794985347,"size":456,"permissions":"rw"}
		]`))
	})
	mux.HandleFunc("/printer/print/start", func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("filename")
		mk.mu.Lock()
		mk.printStarted = append(mk.printStarted, f)
		mk.mu.Unlock()
		_, _ = w.Write([]byte(`"ok"`))
	})
	mux.HandleFunc("/server/files/gcodes/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/server/files/gcodes/")
		switch p {
		case "a.gcode":
			_, _ = w.Write([]byte("; estimated printing time (normal mode) = 2h 29m 52s\nG28\n"))
		case "sub/b.gcode":
			_, _ = w.Write([]byte("G28\n"))
		case ".thumbs/a-300x300.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/server/files/thumbnails", func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("filename")
		switch f {
		case "a.gcode":
			_, _ = w.Write([]byte(`[
				{"width":32,"height":32,"thumbnail_path":".thumbs/a-32x32.png"},
				{"width":300,"height":300,"thumbnail_path":".thumbs/a-300x300.png"}
			]`))
		case "notfound.gcode":
			_, _ = w.Write([]byte(`[
				{"width":300,"height":300,"thumbnail_path":".thumbs/gone.png"}
			]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	// WS: JSON-RPC. Отвечает на subscribe (снапшот print_stats) и
	// delete_file; на subscribe дополнительно высылает queued statusPush.
	mux.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var req struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := c.ReadJSON(&req); err != nil {
				return
			}
			switch req.Method {
			case "printer.objects.subscribe":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      *req.ID,
					"result": map[string]any{
						"eventtime": 1.0,
						"status": map[string]any{
							"print_stats": map[string]any{
								"state": "standby", "filename": "",
							},
						},
					},
				}
				_ = c.WriteJSON(resp)
				// Выслали queued-статусы (тест WatchStatus).
				mk.mu.Lock()
				queued := mk.statusPush
				mk.statusPush = nil
				mk.mu.Unlock()
				go func() {
					for _, s := range queued {
						time.Sleep(10 * time.Millisecond)
						notif := map[string]any{
							"jsonrpc": "2.0",
							"method":  "notify_status_update",
							"params":  []any{mustObj(s), 1.0},
						}
						_ = c.WriteJSON(notif)
					}
				}()
			case "server.files.delete_file":
				var p struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(req.Params, &p)
				mk.mu.Lock()
				mk.deletePaths = append(mk.deletePaths, p.Path)
				errMsg := mk.deleteErr
				mk.mu.Unlock()
				if errMsg != "" {
					_ = c.WriteJSON(map[string]any{
						"jsonrpc": "2.0", "id": *req.ID,
						"error": map[string]any{"code": -32000, "message": errMsg},
					})
				} else {
					_ = c.WriteJSON(map[string]any{
						"jsonrpc": "2.0", "id": *req.ID,
						"result": map[string]any{"action": "delete_file"},
					})
				}
			default:
				mk.mu.Lock()
				mk.rpcMethods = append(mk.rpcMethods, req.Method)
				mk.mu.Unlock()
				if req.ID != nil {
					_ = c.WriteJSON(map[string]any{
						"jsonrpc": "2.0", "id": *req.ID, "result": "ok",
					})
				}
			}
		}
	})

	mk.srv = httptest.NewServer(mux)
	t.Cleanup(mk.srv.Close)
	return mk
}

func (mk *testMoonraker) client() *Client {
	base := mk.srv.URL
	ws := "ws" + strings.TrimPrefix(base, "http") + "/websocket"
	return NewForTest(base, ws)
}

// mustObj превращает []byte в map для вставки в params-массив уведомления.
func mustObj(raw []byte) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func TestInfo(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	// Версия сборки NebulaOS — из /machine/nebulaos/info.
	if c.firmware != "1.0.2" {
		t.Errorf("firmware = %q, want 1.0.2 (nebulaos_version)", c.firmware)
	}
}

func TestInfoFirmwareFallback(t *testing.T) {
	mk := newTestMoonraker(t)
	mk.nebulaosInfo404 = true
	c := mk.client()
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	// /machine/nebulaos/info недоступен — фолбэк на klipper_version.
	if c.firmware != "v0.13.1" {
		t.Errorf("firmware = %q, want v0.13.1 (klipper_version)", c.firmware)
	}
}

func TestListFiles(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	files, err := c.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("файлов = %d, want 2", len(files))
	}
	// Корневой файл: Path = gcodes (без хвостового "/").
	if files[0].Path != "gcodes" || files[0].Name != "a.gcode" ||
		files[0].Size != 123 || files[0].Epoch != 1794988291 {
		t.Errorf("корневой файл = %+v", files[0])
	}
	// Файл в подпапке: Path = gcodes/sub.
	if files[1].Path != "gcodes/sub" || files[1].Name != "b.gcode" ||
		files[1].Size != 456 {
		t.Errorf("файл в подпапке = %+v", files[1])
	}
}

func TestDeleteFile(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	// Печататься/удаляться: path приходит от слоя приложения с префиксом gcodes/.
	if err := c.DeleteFile(context.Background(), "gcodes/sub", "b.gcode"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	mk.mu.Lock()
	paths := append([]string(nil), mk.deletePaths...)
	mk.mu.Unlock()
	if len(paths) != 1 || paths[0] != "gcodes/sub/b.gcode" {
		t.Fatalf("delete_file получен с path = %v, want [gcodes/sub/b.gcode]", paths)
	}

	// Ошибка Moonraker (файла нет) — честно пробрасывается.
	mk.mu.Lock()
	mk.deleteErr = "File not found"
	mk.mu.Unlock()
	if err := c.DeleteFile(context.Background(), "gcodes", "nope.gcode"); err == nil {
		t.Error("DeleteFile при ошибке Moonraker: ожидалась ошибка")
	}
}

func TestPrintFile(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	if err := c.PrintFile(context.Background(), "gcodes/sub", "b.gcode"); err != nil {
		t.Fatalf("PrintFile: %v", err)
	}
	mk.mu.Lock()
	started := append([]string(nil), mk.printStarted...)
	mk.mu.Unlock()
	// filename — относительно gcode-корня (без префикса gcodes/).
	if len(started) != 1 || started[0] != "sub/b.gcode" {
		t.Fatalf("print/start filename = %v, want [sub/b.gcode]", started)
	}
}

func TestDownloadGcode(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	rc, err := c.DownloadGcode(context.Background(), "gcodes/sub", "b.gcode")
	if err != nil {
		t.Fatalf("DownloadGcode: %v", err)
	}
	body, _ := io.ReadAll(rc)
	rc.Close()
	if !strings.Contains(string(body), "G28") {
		t.Errorf("DownloadGcode body = %q", body)
	}
	// Несуществующий файл — 404 → ErrNotFound (окончательный, без ретраев).
	if _, err := c.DownloadGcode(context.Background(), "gcodes", "nope.gcode"); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadGcode 404: err = %v, want ErrNotFound", err)
	}
}

func TestDownloadThumbnail(t *testing.T) {
	mk := newTestMoonraker(t)
	c := mk.client()
	// Берётся самое большое превью (300×300, не 32×32).
	rc, err := c.DownloadThumbnail(context.Background(), "gcodes", "a.gcode")
	if err != nil {
		t.Fatalf("DownloadThumbnail: %v", err)
	}
	png, _ := io.ReadAll(rc)
	rc.Close()
	if len(png) == 0 || png[0] != 0x89 {
		t.Error("пустое или не-PNG превью")
	}
	// Превью нет в списке — ErrNotFound.
	if _, err := c.DownloadThumbnail(context.Background(), "gcodes", "nothumb.gcode"); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadThumbnail без превью: err = %v, want ErrNotFound", err)
	}
	// Превью есть в списке, но файл не отдаётся (404) — тоже ErrNotFound.
	if _, err := c.DownloadThumbnail(context.Background(), "gcodes", "notfound.gcode"); !errors.Is(err, printer.ErrNotFound) {
		t.Errorf("DownloadThumbnail 404-файл: err = %v, want ErrNotFound", err)
	}
}

// Полный цикл WatchStatus: снапшот (standby) → дельты printing/paused/complete.
// PrintFile приводится к формату слоя приложения (префикс gcodes/). Ключевой
// кейс — пауза: дельта содержит только state, filename должен сохраниться
// из дельты печати.
func TestWatchStatus(t *testing.T) {
	mk := newTestMoonraker(t)
	mk.mu.Lock()
	mk.statusPush = [][]byte{
		[]byte(`{"print_stats":{"state":"printing","filename":"sub/b.gcode"}}`),
		[]byte(`{"print_stats":{"state":"paused"}}`),
		[]byte(`{"print_stats":{"state":"complete","filename":""}}`),
	}
	mk.mu.Unlock()
	c := mk.client()
	c.firmware = "1.0.2"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan printer.DeviceStatus, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- c.WatchStatus(ctx, func(ds printer.DeviceStatus) { events <- ds })
	}()

	want := []printer.DeviceStatus{
		{Firmware: "1.0.2"}, // снапшот: standby
		{Printing: true, PrintFile: "gcodes/sub/b.gcode", Firmware: "1.0.2"}, // печать
		{Printing: true, PrintFile: "gcodes/sub/b.gcode", Firmware: "1.0.2"}, // пауза: filename сохранён
		{Firmware: "1.0.2"}, // complete: не печатает, PrintFile сброшен
	}
	for i, w := range want {
		select {
		case ds := <-events:
			if ds != w {
				t.Fatalf("событие %d = %+v, want %+v", i, ds, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("событие %d не пришло за 3 с (ожидал %+v)", i, w)
		}
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, context.Canceled) &&
			!strings.Contains(err.Error(), "context canceled") {
			t.Errorf("WatchStatus после cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WatchStatus не завершилась после cancel")
	}
}
