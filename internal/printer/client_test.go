package printer

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

	"github.com/gorilla/websocket"
)

// --- Тестовый принтер: HTTP + WS на одном httptest-сервере по проверенному
// контракту (docs/api-contract.md). ---

const (
	// Как в реальном ответе после JSON-unescape (принтер на проводе
	// экранирует слэши как \/, Go-строка — с чистыми слэшами).
	testFileInfo = `/usr/data/printer_data/gcodes:a.gcode:123:0.100000:1794988291:3048:/tmp/creality/local_gcode/humbnail/a.png;` +
		`;` + // пустая запись — должна пропускаться
		`/usr/data/printer_data/gcodes:b.gcode:456:0.200000:1794985347:6540:/tmp/creality/local_gcode/humbnail/b.png`
	testFullState = `{ "model": "F005", "hostname": "Ender3V3KE-32F7", "printProgress": 25, "printFileName": "/usr/data/printer_data/gcodes/a.gcode" }`
)

type testPrinter struct {
	srv      *httptest.Server
	mu       sync.Mutex
	received []string
}

func newTestPrinter(t *testing.T) *testPrinter {
	t.Helper()
	tp := &testPrinter{}

	up := websocket.Upgrader{}
	mux := http.NewServeMux()

	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{ "mac": "FCEE110432F7", "model": "F005" }`))
	})
	mux.HandleFunc("/downloads/gcode/a.gcode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("; estimated printing time (normal mode) = 2h 29m 52s\nG28\n"))
	})
	mux.HandleFunc("/downloads/humbnail/a.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	})
	// WS: отвечает на reqGcodeFile (как реальный принтер — сначала push
	// состояния, потом retGcodeFileInfo).
	mux.HandleFunc("/wsl", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			tp.mu.Lock()
			tp.received = append(tp.received, string(msg))
			tp.mu.Unlock()
			if strings.Contains(string(msg), "reqGcodeFile") {
				_ = c.WriteMessage(websocket.TextMessage, []byte(testFullState))
				body, _ := json.Marshal(map[string]any{
					"retGcodeFileInfo": map[string]any{"totalNum": 2, "fileInfo": testFileInfo},
				})
				_ = c.WriteMessage(websocket.TextMessage, body)
			}
		}
	})

	// WS: мусорные ответы (тест некорректного JSON).
	mux.HandleFunc("/wsbad", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage()
		_ = c.WriteMessage(websocket.TextMessage, []byte(`not a json at all`))
	})

	// WS: авто-пуши дельт (тест WatchState/merge).
	mux.HandleFunc("/wsstate", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = c.WriteMessage(websocket.TextMessage, []byte(`{"printProgress":25,"printFileName":"a.gcode"}`))
			time.Sleep(20 * time.Millisecond)
			_ = c.WriteMessage(websocket.TextMessage, []byte(`{"nozzleTemp":"210.43","printLeftTime":7581}`))
		}()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})

	tp.srv = httptest.NewServer(mux)
	t.Cleanup(tp.srv.Close)
	return tp
}

// client возвращает Client, нацеленный на тестовый принтер (HTTP и WS на
// одном тестовом сервере — поэтому wsURL переопределяется явно).
func (tp *testPrinter) client() *Client {
	base := tp.srv.URL // http://127.0.0.1:port
	ws := "ws" + strings.TrimPrefix(base, "http") + "/wsl"
	return NewForTest(base, ws)
}

func (tp *testPrinter) clientWS(wsPath string) *Client {
	base := tp.srv.URL
	ws := "ws" + strings.TrimPrefix(base, "http") + wsPath
	return NewForTest(base, ws)
}

func (tp *testPrinter) receivedMsgs() []string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	out := make([]string, len(tp.received))
	copy(out, tp.received)
	return out
}

// --- Тесты ---

func TestInfo(t *testing.T) {
	tp := newTestPrinter(t)
	info, err := tp.client().Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" || info.MAC != "FCEE110432F7" {
		t.Fatalf("Info = %+v, want model F005 mac FCEE110432F7", info)
	}
}

func TestListFiles(t *testing.T) {
	tp := newTestPrinter(t)
	files, err := tp.client().ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ListFiles = %d файлов, want 2 (пустая запись пропускается): %+v", len(files), files)
	}
	f := files[0]
	if f.Path != "/usr/data/printer_data/gcodes" {
		t.Errorf("Path = %q", f.Path)
	}
	if f.Name != "a.gcode" {
		t.Errorf("Name = %q", f.Name)
	}
	if f.Size != 123 {
		t.Errorf("Size = %d, want 123", f.Size)
	}
	if f.Layer != 0.1 {
		t.Errorf("Layer = %v, want 0.1", f.Layer)
	}
	if f.Epoch != 1794988291 {
		t.Errorf("Epoch = %d, want 1794988291", f.Epoch)
	}
	if f.FilamentMM != 3048 {
		t.Errorf("FilamentMM = %d, want 3048", f.FilamentMM)
	}
	if f.Thumb != "/tmp/creality/local_gcode/humbnail/a.png" {
		t.Errorf("Thumb = %q", f.Thumb)
	}
}

func TestListFilesBadJSON(t *testing.T) {
	tp := newTestPrinter(t)
	c := tp.clientWS("/wsbad")
	done := make(chan error, 1)
	go func() {
		_, err := c.ListFiles(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ListFiles с мусорным JSON: ожидалась ошибка, получена nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ListFiles не завершилась за 10 с (panic или зависание?)")
	}
}

func TestDeleteFile(t *testing.T) {
	tp := newTestPrinter(t)
	path := "/usr/data/printer_data/gcodes"
	if err := tp.client().DeleteFile(context.Background(), path, "a.gcode"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	// Клиент закрывает WS сразу после отправки (принтер не шлёт ack), поэтому
	// сервер-обработчик может дочитать кадр с задержкой — поллинг с дедлайном.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range tp.receivedMsgs() {
			if strings.Contains(m, `"deleteprt:`+path+`/a.gcode"`) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("сервер не получил deleteprt-операцию; получено: %v", tp.receivedMsgs())
}

func TestPrintFile(t *testing.T) {
	tp := newTestPrinter(t)
	path := "/usr/data/printer_data/gcodes"
	if err := tp.client().PrintFile(context.Background(), path, "a.gcode"); err != nil {
		t.Fatalf("PrintFile: %v", err)
	}
	// Как с deleteprt: ack нет, клиент закрывает WS сразу после отправки —
	// поллинг с дедлайном.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range tp.receivedMsgs() {
			if strings.Contains(m, `"printprt:`+path+`/a.gcode"`) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("сервер не получил printprt-операцию; получено: %v", tp.receivedMsgs())
}

func TestDownloadGcode(t *testing.T) {
	tp := newTestPrinter(t)
	rc, err := tp.client().DownloadGcode(context.Background(), "a.gcode")
	if err != nil {
		t.Fatalf("DownloadGcode: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "estimated printing time") {
		t.Fatalf("DownloadGcode body = %q", body)
	}
}

func TestDownloadGcodeNotFound(t *testing.T) {
	tp := newTestPrinter(t)
	rc, err := tp.client().DownloadGcode(context.Background(), "missing.gcode")
	if err == nil {
		rc.Close()
		t.Fatal("DownloadGcode для несуществующего файла: ожидалась ошибка")
	}
}

func TestDownloadThumbnail(t *testing.T) {
	tp := newTestPrinter(t)
	rc, err := tp.client().DownloadThumbnail(context.Background(), "a.gcode")
	if err != nil {
		t.Fatalf("DownloadThumbnail: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if len(body) == 0 {
		t.Fatal("пустое превью")
	}
}

func TestWatchStateMerge(t *testing.T) {
	tp := newTestPrinter(t)
	c := tp.clientWS("/wsstate")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	merged := make(chan *State, 8)
	errc := make(chan error, 1)
	go func() {
		errc <- c.WatchState(ctx, func(s *State) { merged <- s })
	}()

	// Ждём, пока дельты не смержатся: printProgress=25 + nozzleTemp=210.43,
	// и при этом printProgress НЕ сброшен второй дельтой.
	var last *State
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case s := <-merged:
			last = s
			p, _ := s.Int("printProgress")
			fn, _ := s.Str("printFileName")
			nt, _ := s.Str("nozzleTemp")
			if p == 25 && fn == "a.gcode" && nt != "" {
				break loop
			}
		case <-deadline:
			t.Fatalf("merge дельт не сложился за 5 с; last=%v", last)
		}
	}
	cancel()
	// WatchState должна корректно завершиться по отмене ctx (не panic).
	select {
	case err := <-errc:
		if err != nil && !isContextError(err) {
			t.Errorf("WatchState после cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WatchState не завершилась после cancel ctx")
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "closed network connection")
}

func TestClientTimeout(t *testing.T) {
	// Сервер, который никогда не отвечает: клиент должен сдаться за ≤5 с.
	// Ждём отмены запроса клиентом, чтобы не висеть на Close тестового сервера.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	c := New(slow.URL) // ws не используется для /info

	start := time.Now()
	_, err := c.Info(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Info против висящего сервера: ожидалась ошибка")
	}
	if elapsed > 7*time.Second {
		t.Fatalf("таймаут сработал за %v, want ≤5 с (+запас)", elapsed)
	}
}
