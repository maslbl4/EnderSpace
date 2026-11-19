// Package printer — клиент принтера Creality Ender-3 V3 KE (F005).
// Протокол: HTTP (порт 80) + WebSocket (порт 9999). Контракт: docs/api-contract.md.
package printer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// wsPort — порт кастомного WebSocket-сервера Creality.
const wsPort = "9999"

// readTimeout — лимит ожидания одного WS-ответа (список файлов).
const readTimeout = 5 * time.Second

// Client — гибридный HTTP+WS клиент одного принтера.
type Client struct {
	base   string       // http://<ip>
	wsURL  string       // ws://<ip>:9999
	http   *http.Client // для /info (с жёстким таймаутом)
	dialer *websocket.Dialer
}

// New создаёт клиент по базовому HTTP-адресу принтера (http://<ip>).
// WS-URL выводится автоматически (ws://<ip>:9999).
func New(base string) *Client {
	host := hostOnly(base)
	return &Client{
		base:   strings.TrimRight(base, "/"),
		wsURL:  "ws://" + host + ":" + wsPort,
		http:   &http.Client{Timeout: 5 * time.Second},
		dialer: &websocket.Dialer{HandshakeTimeout: 5 * time.Second},
	}
}

// NewForTest создаёт клиент с явным WS-URL (для тестов, где HTTP и WS на
// одном httptest-сервере).
func NewForTest(base, wsURL string) *Client {
	c := New(base)
	c.wsURL = wsURL
	return c
}

// hostOnly извлекает IP/хост из http-адреса (без порта).
func hostOnly(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return strings.TrimPrefix(strings.TrimPrefix(strings.TrimRight(base, "/"), "http://"), "https://")
	}
	host := u.Hostname()
	if host == "" {
		return u.Host
	}
	return host
}

// Info — GET /info (mac, модель).
func (c *Client) Info(ctx context.Context) (Info, error) {
	var raw struct {
		MAC   string `json:"mac"`
		Model string `json:"model"`
	}
	if err := c.getJSON(ctx, "/info", &raw); err != nil {
		return Info{}, err
	}
	return Info{MAC: raw.MAC, Model: raw.Model}, nil
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

// ListFiles — WS reqGcodeFile. Ответ приходит асинхронно среди push-сообщений,
// поэтому читаем, пока не встретим retGcodeFileInfo (или таймаут).
func (c *Client) ListFiles(ctx context.Context) ([]RawFile, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage,
		[]byte(`{"method":"get","params":{"reqGcodeFile":1}}`)); err != nil {
		return nil, fmt.Errorf("ws: отправка reqGcodeFile: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return nil, fmt.Errorf("ws: нет ответа на reqGcodeFile за %s", readTimeout)
			}
			return nil, fmt.Errorf("ws: чтение: %w", err)
		}
		files, done, err := tryParseFileList(data)
		if err != nil {
			return nil, err
		}
		if done {
			return files, nil
		}
		// Не список (push состояния) — читаем дальше.
	}
}

// tryParseFileList: data — JSON-объект; если есть retGcodeFileInfo — разбираем
// список и done=true; иначе (push состояния) done=false. Некорректный JSON — ошибка.
func tryParseFileList(data []byte) ([]RawFile, bool, error) {
	var payload struct {
		Ret *struct {
			TotalNum int    `json:"totalNum"`
			FileInfo string `json:"fileInfo"`
		} `json:"retGcodeFileInfo"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, false, fmt.Errorf("ws: сообщение не распарсилось как JSON: %w", err)
	}
	if payload.Ret == nil {
		return nil, false, nil
	}
	return parseFileInfo(payload.Ret.FileInfo)
}

// parseFileInfo разбирает строку "rec;rec;..." где rec = path:name:size:layer:epoch:mm:thumb.
func parseFileInfo(fileInfo string) ([]RawFile, bool, error) {
	var out []RawFile
	for _, rec := range strings.Split(fileInfo, ";") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue // пустые записи между разделителями
		}
		f := strings.Split(rec, ":")
		if len(f) < 7 {
			return nil, false, fmt.Errorf("ws: запись списка короче 7 полей: %q", rec)
		}
		var rf RawFile
		rf.Path = f[0]
		rf.Name = f[1]
		size, err := parseInt64(f[2])
		if err != nil {
			return nil, false, fmt.Errorf("ws: поле size %q: %w", f[2], err)
		}
		rf.Size = size
		layer, err := parseFloat(f[3])
		if err != nil {
			return nil, false, fmt.Errorf("ws: поле layer %q: %w", f[3], err)
		}
		rf.Layer = layer
		epoch, err := parseInt64(f[4])
		if err != nil {
			return nil, false, fmt.Errorf("ws: поле epoch %q: %w", f[4], err)
		}
		rf.Epoch = epoch
		mm, err := parseInt(f[5])
		if err != nil {
			return nil, false, fmt.Errorf("ws: поле length %q: %w", f[5], err)
		}
		rf.FilamentMM = mm
		rf.Thumb = f[6]
		out = append(out, rf)
	}
	return out, true, nil
}

// DeleteFile — WS set opGcodeFile "deleteprt:<path>/<name>". Принтер не шлёт
// ack на удаление (UI принтера тоже полагается на повторный запрос списка),
// поэтому успех = успешно отправили.
func (c *Client) DeleteFile(ctx context.Context, path, name string) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	op := "deleteprt:" + path + "/" + name
	msg, _ := json.Marshal(map[string]any{
		"method": "set",
		"params": map[string]any{"opGcodeFile": op},
	})
	return conn.WriteMessage(websocket.TextMessage, msg)
}

// DownloadGcode — GET /downloads/gcode/<name> (стриминг, весь файл).
// Имя экранируется: в названиях бывают пробелы и кириллица.
func (c *Client) DownloadGcode(ctx context.Context, name string) (io.ReadCloser, error) {
	return c.download(ctx, "/downloads/gcode/"+url.PathEscape(name))
}

// DownloadThumbnail — GET /downloads/humbnail/<base>.png (принтер называет
// превью по имени без суффикса .gcode: orcs_PLA.gcode → orcs_PLA.png).
func (c *Client) DownloadThumbnail(ctx context.Context, name string) (io.ReadCloser, error) {
	base := strings.TrimSuffix(name, ".gcode")
	return c.download(ctx, "/downloads/humbnail/"+url.PathEscape(base)+".png")
}

func (c *Client) download(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return resp.Body, nil
}

// WatchState — WS: читает периодические push-сообщения состояния, мерджит их
// в State и на каждое вызывает fn. Возвращает, когда ctx отменён или WS упал.
func (c *Client) WatchState(ctx context.Context, fn func(*State)) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Отмена ctx должна будить ReadMessage немедленно: закрываем conn.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	state := NewState()
	// Принтер шлёт push каждые ~1–2 с; даём щедрый read-таймаут на «тишину».
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Таймаут тишины или обрыв — пробрасываем, чтобы вызывающий мог
			// переподключиться.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return fmt.Errorf("ws: тишина больше read-таймаута: %w", err)
			}
			return fmt.Errorf("ws: чтение: %w", err)
		}
		if err := state.Merge(data); err != nil {
			// Push не-JSON игнорируем, не роняем наблюдение.
			continue
		}
		fn(state)
	}
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

func parseInt64(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func parseInt(s string) (int, error) {
	v, err := parseInt64(s)
	return int(v), err
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	if err != nil {
		return 0, err
	}
	return v, nil
}
