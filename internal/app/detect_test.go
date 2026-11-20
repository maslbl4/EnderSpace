package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
		nebulaos.NewForTest(deadAddr, "ws://127.0.0.1:1/websocket"))
	info, err := dp.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	if dp.active != dp.creality {
		t.Error("активный драйвер = nebulaos, want creality")
	}
}

// NebulaOS: зонд Creality быстро падает (на :80 — Mainsail, /info там 404),
// детектится Moonraker; последующие вызовы делегируются драйверу nebulaos.
func TestDetectDriverChoosesNebulaos(t *testing.T) {
	mrk := moonrakerServer(t)
	dp := newDetectingPrinter(creality.New(mrk.URL),
		nebulaos.NewForTest(mrk.URL, "ws://127.0.0.1:1/websocket"))
	info, err := dp.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != "F005" {
		t.Errorf("Model = %q, want F005", info.Model)
	}
	if dp.active != dp.nebulaos {
		t.Error("активный драйвер = creality, want nebulaos")
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
		nebulaos.NewForTest(deadAddr, "ws://127.0.0.1:1/websocket"))
	_, err := dp.Info(context.Background())
	if err == nil {
		t.Fatal("Info: ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "creality") || !strings.Contains(err.Error(), "nebulaos") {
		t.Errorf("ошибка без обоих драйверов: %v", err)
	}
}
