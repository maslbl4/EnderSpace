package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"ender-space/internal/printer"
	"ender-space/internal/printer/creality"
	"ender-space/internal/printer/nebulaos"
)

// detectDriver — фабрика принтера по умолчанию: одна и та же F005 может
// работать со стоковой прошивкой Creality (HTTP :80 + WS :9999) и с NebulaOS
// (Klipper + Moonraker, :7125), поэтому драйвер заранее не известен.
// Авто-детект — в первом вызове Info (Connect): сначала пробует Creality
// (GET /info), не вышло — NebulaOS (GET /server/info).
func detectDriver(ip string) printer.Printer {
	return newDetectingPrinter(creality.New("http://"+ip), nebulaos.New(ip))
}

// detectingPrinter — printer.Printer поверх двух драйверов: активный
// выбирается в Info (первый вызов слоя приложения при подключении), все
// остальные методы делегируют ему.
type detectingPrinter struct {
	mu       sync.Mutex
	active   printer.Printer
	creality *creality.Client
	nebulaos *nebulaos.Client
}

var _ printer.Printer = (*detectingPrinter)(nil)

func newDetectingPrinter(c *creality.Client, n *nebulaos.Client) *detectingPrinter {
	return &detectingPrinter{creality: c, nebulaos: n}
}

// driver — выбранный драйвер; ошибка, если Info ещё не вызывали.
func (d *detectingPrinter) driver() (printer.Printer, error) {
	d.mu.Lock()
	c := d.active
	d.mu.Unlock()
	if c == nil {
		return nil, errors.New("драйвер принтера не определён: Info ещё не вызывался")
	}
	return c, nil
}

func (d *detectingPrinter) setActive(c printer.Printer) {
	d.mu.Lock()
	if d.active == nil {
		d.active = c
	}
	d.mu.Unlock()
}

// Info детектит драйвер (первый вызов) или повторяет проверку активным.
func (d *detectingPrinter) Info(ctx context.Context) (printer.Info, error) {
	if c, err := d.driver(); err == nil {
		return c.Info(ctx)
	}

	// Creality первой: на NebulaOS порт 80 отдаёт веб-интерфейс Mainsail,
	// где GET /info даёт 404/HTML, — зонд падает быстро и уходит на Moonraker.
	info, crErr := d.creality.Info(ctx)
	if crErr == nil {
		d.setActive(d.creality)
		return info, nil
	}
	info, nbErr := d.nebulaos.Info(ctx)
	if nbErr != nil {
		return printer.Info{}, fmt.Errorf(
			"не удалось определить драйвер принтера: creality: %v; nebulaos: %v", crErr, nbErr)
	}
	d.setActive(d.nebulaos)
	return info, nil
}

func (d *detectingPrinter) ListFiles(ctx context.Context) ([]printer.File, error) {
	c, err := d.driver()
	if err != nil {
		return nil, err
	}
	return c.ListFiles(ctx)
}

func (d *detectingPrinter) DeleteFile(ctx context.Context, path, name string) error {
	c, err := d.driver()
	if err != nil {
		return err
	}
	return c.DeleteFile(ctx, path, name)
}

func (d *detectingPrinter) PrintFile(ctx context.Context, path, name string) error {
	c, err := d.driver()
	if err != nil {
		return err
	}
	return c.PrintFile(ctx, path, name)
}

func (d *detectingPrinter) DownloadGcode(ctx context.Context, path, name string) (io.ReadCloser, error) {
	c, err := d.driver()
	if err != nil {
		return nil, err
	}
	return c.DownloadGcode(ctx, path, name)
}

func (d *detectingPrinter) DownloadThumbnail(ctx context.Context, path, name string) (io.ReadCloser, error) {
	c, err := d.driver()
	if err != nil {
		return nil, err
	}
	return c.DownloadThumbnail(ctx, path, name)
}

func (d *detectingPrinter) WatchStatus(ctx context.Context, fn func(printer.DeviceStatus)) error {
	c, err := d.driver()
	if err != nil {
		return err
	}
	return c.WatchStatus(ctx, fn)
}
