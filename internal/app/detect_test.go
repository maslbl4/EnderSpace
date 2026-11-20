package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ender-space/internal/printer/creality"
	"ender-space/internal/printer/nebulaos"
)

// deadAddr — адрес, на который никто не слушает (быстрый отказ соединения).
const deadAddr = "http://127.0.0.1:1"

// crealityServer — httptest в форме стоковой прошивки F005 (GET /info).
func crealityServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			_, _ = w.Write([]byte(`{"mac":"FCEE110432F7","model":"F005"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// moonrakerServer — httptest в форме Moonraker (NebulaOS).
func moonrakerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/server/info":
			_, _ = w.Write([]byte(`{"hostname":"nebulaos-32f7","klipper_version":"v0.13.1"}`))
		case "/machine/nebulaos/info":
			_, _ = w.Write([]byte(`{"build":{"available":true,"nebulaos_version":"1.0.2"}}`))
		case "/server/files/list":
			_, _ = w.Write([]byte(`[{"path":"a.gcode","modified":1794988291,"size":123}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Creality первой: сервер с /info детектится как стоковая прошивка.
func TestDetectDriverChoosesCreality(t *testing.T) {
	dp := newDetectingPrinter(creality.New(crealityServer(t).URL),
		nebulaos.NewForTest(deadAddr, "ws://127.0.0.1:1/websocket"), "")
	info, err := dp.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	if dp.DriverName() != DriverCreality {
		t.Errorf("DriverName = %q, want %q", dp.DriverName(), DriverCreality)
	}
}

// NebulaOS: зонд Creality быстро падает (на :80 — Mainsail, /info там 404),
// детектится Moonraker; последующие вызовы делегируются драйверу nebulaos.
func TestDetectDriverChoosesNebulaos(t *testing.T) {
	mrk := moonrakerServer(t)
	dp := newDetectingPrinter(creality.New(mrk.URL),
		nebulaos.NewForTest(mrk.URL, "ws://127.0.0.1:1/websocket"), "")
	info, err := dp.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	if dp.DriverName() != DriverNebulaOS {
		t.Errorf("DriverName = %q, want %q", dp.DriverName(), DriverNebulaOS)
	}

	// Делегирование: ListFiles ушло в драйвер Moonraker.
	files, err := dp.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "gcodes" || files[0].Name != "a.gcode" {
		t.Errorf("ListFiles = %+v, want [gcodes/a.gcode]", files)
	}
}

// Оба зонда упали: ошибка называет оба драйвера.
func TestDetectDriverBothFail(t *testing.T) {
	dp := newDetectingPrinter(creality.New(deadAddr),
		nebulaos.NewForTest(deadAddr, "ws://127.0.0.1:1/websocket"), "")
	_, err := dp.Info(context.Background())
	if err == nil {
		t.Fatal("Info: ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "creality") || !strings.Contains(err.Error(), "nebulaos") {
		t.Errorf("ошибка без обоих драйверов: %v", err)
	}
}

// probeRecorder — httptest, записывающий порядок пришедших зондов.
// Отвечает как NebulaOS: /info (Creality-зонд) — 404, Moonraker-пути — 200.
type probeRecorder struct {
	srv   *httptest.Server
	mu    sync.Mutex
	order []string
}

func newProbeRecorder(t *testing.T) *probeRecorder {
	t.Helper()
	p := &probeRecorder{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.order = append(p.order, r.URL.Path)
		p.mu.Unlock()
		switch r.URL.Path {
		case "/server/info":
			_, _ = w.Write([]byte(`{"hostname":"nebulaos-32f7","klipper_version":"v0.13.1"}`))
		case "/machine/nebulaos/info":
			_, _ = w.Write([]byte(`{"build":{"available":true,"nebulaos_version":"1.0.2"}}`))
		default:
			http.NotFound(w, r) // /info — как на NebulaOS (Mainsail на :80)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *probeRecorder) ordered() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

// Запоминаемый драйвер (lastDriver из конфига) зондится первым: если он
// отвечает — вторая ОС не опрашивается вовсе.
func TestDetectDriverPreferredProbedFirst(t *testing.T) {
	// Порядок по умолчанию: Creality-зонд первым (здесь падает — сервер в
	// форме NebulaOS).
	p := newProbeRecorder(t)
	dp := newDetectingPrinter(creality.New(p.srv.URL),
		nebulaos.NewForTest(p.srv.URL, "ws://127.0.0.1:1/websocket"), "")
	if _, err := dp.Info(context.Background()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got := p.ordered(); len(got) == 0 || got[0] != "/info" {
		t.Errorf("порядок по умолчанию = %v, want /info первым", got)
	}

	// preferred=nebulaos: Creality-зонд не делается — повторное
	// подключение не опрашивает вторую ОС.
	p2 := newProbeRecorder(t)
	dp2 := newDetectingPrinter(creality.New(p2.srv.URL),
		nebulaos.NewForTest(p2.srv.URL, "ws://127.0.0.1:1/websocket"), DriverNebulaOS)
	if _, err := dp2.Info(context.Background()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	got := p2.ordered()
	if len(got) == 0 || got[0] != "/server/info" {
		t.Errorf("порядок с preferred=nebulaos = %v, want /server/info первым", got)
	}
	for _, path := range got {
		if path == "/info" {
			t.Errorf("с preferred=nebulaos всё же был Creality-зонд: %v", got)
			break
		}
	}
}

// Фабрика по умолчанию берёт предпочтение из конфига — и только для IP,
// которым принтер уже подключался.
func TestDefaultPrinterFactoryUsesConfigPreference(t *testing.T) {
	app := NewAppWithClientAndSink(t.TempDir(), nil, nil)
	const ip = "10.0.0.1"

	// Конфиг пуст → порядок по умолчанию: creality первым.
	dp := app.defaultPrinterFactory(ip).(*detectingPrinter)
	if dp.candidates[0].name != DriverCreality {
		t.Errorf("первый кандидат без конфига = %q, want %q", dp.candidates[0].name, DriverCreality)
	}

	// Конфиг: этот IP подключался драйвером nebulaos → он зондится первым.
	app.mu.Lock()
	app.cfg.LastIP = ip
	app.cfg.LastDriver = DriverNebulaOS
	app.mu.Unlock()
	dp = app.defaultPrinterFactory(ip).(*detectingPrinter)
	if dp.candidates[0].name != DriverNebulaOS {
		t.Errorf("первый кандидат с конфигом = %q, want %q", dp.candidates[0].name, DriverNebulaOS)
	}

	// Другой IP → предпочтение не применяется.
	app.mu.Lock()
	app.cfg.LastIP = "10.0.0.2"
	app.mu.Unlock()
	dp = app.defaultPrinterFactory(ip).(*detectingPrinter)
	if dp.candidates[0].name != DriverCreality {
		t.Errorf("первый кандидат для другого IP = %q, want %q", dp.candidates[0].name, DriverCreality)
	}
}
