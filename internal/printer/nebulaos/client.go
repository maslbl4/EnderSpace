// Package nebulaos — драйвер принтера Ender-3 V3 KE под NebulaOS
// (Klipper + Moonraker). В отличие от стоковой прошивки Creality (HTTP :80 +
// кастомный WS :9999), NebulaOS говорит стандартным Moonraker: REST +
// JSON-RPC на :7125 (порт 80 проксирует те же маршруты, но мы обращаемся
// напрямую к 7125). Тот же принтер со стоковой прошивкой — драйвер creality;
// оба реализуют общий интерфейс printer.Printer.
//
// Контракт Moonraker: moonraker.readthedocs.io. Версия NebulaOS берётся из
// расширения /machine/nebulaos/info (build.nebulaos_version) с фолбэком на
// klipper_version из /server/info.
package nebulaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"ender-space/internal/printer"

	"github.com/gorilla/websocket"
)

// gcodeRoot — имя Moonraker-корня G-кодов (printer_data/gcodes). Путь файлов
// в Moonraker — относительно этого корня; драйвер добавляет префикс "gcodes/",
// чтобы "Path/Name" слоя приложения совпадал с print_stats.filename.
const gcodeRoot = "gcodes"

// readTimeout — лимит одного HTTP-запроса / WS-ответа.
const readTimeout = 10 * time.Second

// downloadTimeout — лимит докачки G-кода / превью.
const downloadTimeout = 60 * time.Second

// watchSilenceTimeout — «тишина» WS-наблюдения. В отличие от Creality
// (push каждые ~1–2 с) Moonraker шлёт notify_status_update только при
// изменении подписанных объектов; в простое принтер молчит, и это
// нормально. 5 минут — уже похоже на мёртвое соединение.
const watchSilenceTimeout = 5 * time.Minute

// Client — клиент Moonraker одного принтера.
type Client struct {
	base      string       // http://<ip>:7125
	wsURL     string       // ws://<ip>:7125/websocket
	http      *http.Client // короткие запросы (/server/info и т.п.)
	downloads *http.Client // докачки (длинный таймаут)
	dialer    *websocket.Dialer

	firmware string // версия прошивки (из Info; ставится в каждый DeviceStatus)
}

// New создаёт клиент по IP: Moonraker на :7125.
func New(ip string) *Client {
	host := ip
	return &Client{
		base:      "http://" + host + ":7125",
		wsURL:     "ws://" + host + ":7125/websocket",
		http:      &http.Client{Timeout: readTimeout},
		downloads: &http.Client{Timeout: downloadTimeout},
		dialer:    &websocket.Dialer{HandshakeTimeout: 5 * time.Second},
	}
}

// NewForTest создаёт клиент с явными HTTP/WS-адресами (тесты: httptest).
func NewForTest(base, wsURL string) *Client {
	c := New("test")
	c.base = strings.TrimRight(base, "/")
	c.wsURL = wsURL
	return c
}

// Info — GET /server/info (проверка связи) + версия NebulaOS.
// Модель — F005: NebulaOS шьётся только под Ender-3 V3 KE.
func (c *Client) Info(ctx context.Context) (printer.Info, error) {
	var si struct {
		KlipperVersion string `json:"klipper_version"`
		Hostname       string `json:"hostname"`
	}
	if err := c.getJSON(ctx, "/server/info", &si); err != nil {
		return printer.Info{}, fmt.Errorf("moonraker /server/info: %w", err)
	}
	c.firmware = c.nebulaosVersion(ctx, si.KlipperVersion)
	return printer.Info{Model: "F005"}, nil
}

// nebulaosVersion — версия сборки NebulaOS: /machine/nebulaos/info →
// build.nebulaos_version; нет/ошибка — фолбэк на klipper_version.
func (c *Client) nebulaosVersion(ctx context.Context, fallback string) string {
	var ni struct {
		Build struct {
			Available       bool   `json:"available"`
			NebulaosVersion string `json:"nebulaos_version"`
		} `json:"build"`
	}
	if err := c.getJSON(ctx, "/machine/nebulaos/info", &ni); err == nil &&
		ni.Build.Available && ni.Build.NebulaosVersion != "" {
		return ni.Build.NebulaosVersion
	}
	return fallback
}

// ListFiles — GET /server/files/list?root=gcodes. Путь Moonraker —
// относительно gcode-корня ("sub/a.gcode"); драйвер мапит в File с
// префиксом gcodes/, чтобы "Path/Name" совпал с print_stats.filename.
func (c *Client) ListFiles(ctx context.Context) ([]printer.File, error) {
	var raw []struct {
		Path     string  `json:"path"`
		Size     int64   `json:"size"`
		Modified float64 `json:"modified"`
	}
	if err := c.getJSON(ctx, "/server/files/list?root="+gcodeRoot, &raw); err != nil {
		return nil, err
	}
	out := make([]printer.File, 0, len(raw))
	for _, f := range raw {
		dir, name := path.Split(f.Path)
		sub := strings.TrimSuffix(dir, "/") // "" для корня, "sub" для подпапки
		fp := gcodeRoot
		if sub != "" {
			fp = gcodeRoot + "/" + sub
		}
		out = append(out, printer.File{
			Path:  fp,
			Name:  name,
			Size:  f.Size,
			Epoch: int64(f.Modified),
		})
	}
	return out, nil
}

// relRoot — путь файла относительно gcode-корня (для Moonraker-эндпоинтов):
// "gcodes/sub/a.gcode" → "sub/a.gcode". path приходит от слоя приложения уже
// с префиксом gcodes/ (см. ListFiles).
func relRoot(path, name string) string {
	p := name
	if path != "" {
		p = path + "/" + name
	}
	return strings.TrimPrefix(p, gcodeRoot+"/")
}

// DeleteFile — JSON-RPC server.files.delete_file (только по WS). Принтер
// шлёт ack, поэтому ошибка провала пробрасывается честно.
func (c *Client) DeleteFile(ctx context.Context, p, name string) error {
	rel := relRoot(p, name)
	_, err := c.rpc(ctx, "server.files.delete_file",
		map[string]string{"path": gcodeRoot + "/" + rel})
	return err
}

// PrintFile — POST /printer/print/start?filename=<относительно gcode-корня>.
func (c *Client) PrintFile(ctx context.Context, p, name string) error {
	rel := relRoot(p, name)
	u := c.base + "/printer/print/start?filename=" + url.QueryEscape(rel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("print/start: HTTP %d", resp.StatusCode)
	}
	return nil
}

// DownloadGcode — GET /server/files/gcodes/<rel>.
func (c *Client) DownloadGcode(ctx context.Context, p, name string) (io.ReadCloser, error) {
	return c.download(ctx, "/server/files/"+gcodeRoot+"/"+escPath(relRoot(p, name)))
}

// DownloadThumbnail — превью: список превью файла (GET /server/files/thumbnails)
// → самый большой → GET /server/files/gcodes/<thumbnail_path>. Нет превью →
// printer.ErrNotFound (слой приложения кэширует «превью точно нет»).
func (c *Client) DownloadThumbnail(ctx context.Context, p, name string) (io.ReadCloser, error) {
	rel := relRoot(p, name)
	var thumbs []struct {
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		ThumbnailPath string `json:"thumbnail_path"`
	}
	u := c.base + "/server/files/thumbnails?filename=" + url.QueryEscape(rel)
	if err := c.getJSON(ctx, u[len(c.base):], &thumbs); err != nil {
		return nil, err
	}
	best := ""
	bestArea := 0
	for _, th := range thumbs {
		if th.ThumbnailPath == "" {
			continue
		}
		if area := th.Width * th.Height; area > bestArea {
			bestArea = area
			best = th.ThumbnailPath
		}
	}
	if best == "" {
		return nil, fmt.Errorf("превью %s: %w", rel, printer.ErrNotFound)
	}
	return c.download(ctx, "/server/files/"+gcodeRoot+"/"+escPath(best))
}

func (c *Client) download(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.downloads.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("GET %s: %w", path, printer.ErrNotFound)
		}
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return resp.Body, nil
}

// escPath экранирует каждый сегмент пути по отдельности (PathEscape на весь
// путь сломал бы разделители "/").
func escPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("GET %s: ответ не JSON: %w", path, err)
	}
	return nil
}

// ---- WebSocket / JSON-RPC ----

// jsonrpcMsg — общий кадр JSON-RPC 2.0 (запрос/ответ/уведомление).
type jsonrpcMsg struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message)
}

// rpc — разовый JSON-RPC-вызов по собственному WS-соединению: шлёт запрос,
// читает кадры, пока не получит ответ с нашим id (уведомления пропускает).
func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	id := 1
	if err := conn.WriteJSON(jsonrpcMsg{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("ws: запись %s: %w", method, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	for {
		var msg jsonrpcMsg
		if err := conn.ReadJSON(&msg); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return nil, fmt.Errorf("ws: нет ответа на %s за %s", method, readTimeout)
			}
			return nil, fmt.Errorf("ws: чтение: %w", err)
		}
		if msg.ID != nil && *msg.ID == id {
			if msg.Error != nil {
				return nil, msg.Error
			}
			return msg.Result, nil
		}
		// Уведомления до ответа — пропускаем.
	}
}

// printStats — текущие поля print_stats, накопленные из снимка + дельт.
type printStats struct {
	State    string
	Filename string
}

// psDelta — дельта print_stats: не-nil поле изменилось.
type psDelta struct {
	State    *string `json:"state"`
	Filename *string `json:"filename"`
}

func (s *printStats) merge(d psDelta) {
	if d.State != nil {
		s.State = *d.State
	}
	if d.Filename != nil {
		s.Filename = *d.Filename
	}
}

// statusFromPrintStats — нормализует print_stats в DeviceStatus. «Печатает» —
// state ∈ {printing, paused} (пауза — работа ещё идёт). PrintFile приводится
// к формату "Path/Name" слоя приложения (префикс gcodes/).
func (c *Client) statusFromPrintStats(ps printStats) printer.DeviceStatus {
	ds := printer.DeviceStatus{Firmware: c.firmware}
	if ps.State == "printing" || ps.State == "paused" {
		ds.Printing = true
		if ps.Filename != "" {
			ds.PrintFile = gcodeRoot + "/" + ps.Filename
		}
	}
	return ds
}

// WatchStatus — WS: подписывается на print_stats, шлёт fn на каждый снимок
// (ответ subscribe) и каждое обновление (notify_status_update). Возвращает по
// отмене ctx или обрыву связи.
func (c *Client) WatchStatus(ctx context.Context, fn func(printer.DeviceStatus)) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	ps := printStats{}
	id := 1
	sub := jsonrpcMsg{
		JSONRPC: "2.0", ID: &id, Method: "printer.objects.subscribe",
		Params: map[string]any{"objects": map[string]any{"print_stats": nil}},
	}
	if err := conn.WriteJSON(sub); err != nil {
		return fmt.Errorf("ws: subscribe: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(watchSilenceTimeout))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var msg jsonrpcMsg
		if err := conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("ws: чтение: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(watchSilenceTimeout))
		switch {
		case msg.ID != nil && *msg.ID == id:
			if msg.Error != nil {
				return msg.Error
			}
			mergeSubscribeResult(&ps, msg.Result)
			fn(c.statusFromPrintStats(ps))
		case msg.Method == "notify_status_update":
			mergeNotification(&ps, msg.Params)
			fn(c.statusFromPrintStats(ps))
		}
	}
}

// mergeSubscribeResult — result = {eventtime, status:{print_stats:{...}}}.
func mergeSubscribeResult(ps *printStats, raw json.RawMessage) {
	var res struct {
		Status struct {
			PrintStats psDelta `json:"print_stats"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	ps.merge(res.Status.PrintStats)
}

// mergeNotification — params = [{print_stats:{...}}, timestamp].
func mergeNotification(ps *printStats, raw any) {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return
	}
	b, err := json.Marshal(arr[0])
	if err != nil {
		return
	}
	var obj struct {
		PrintStats psDelta `json:"print_stats"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return
	}
	ps.merge(obj.PrintStats)
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	conn, resp, err := c.dialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("ws %s: %w (HTTP %d)", c.wsURL, err, resp.StatusCode)
		}
		return nil, fmt.Errorf("ws %s: %w", c.wsURL, err)
	}
	return conn, nil
}
