package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"ender-space/internal/printer"
	"ender-space/internal/printer/creality"
	"ender-space/internal/printer/nebulaos"
)

// Имена драйверов в конфиге (lastDriver) и в ошибках детекта.
const (
	DriverCreality = "creality"
	DriverNebulaOS = "nebulaos"
)

// detectDriver — фабрика принтера с авто-детектом ОС: одна и та же F005
// может работать со стоковой прошивкой Creality (HTTP :80 + WS :9999) и с
// NebulaOS (Klipper + Moonraker, :7125). Порядок зондов по умолчанию —
// Creality → NebulaOS; preferred (драйвер, которым IP уже подключался,
// из конфига) зондится первым, поэтому повторное подключение не
// опрашивает вторую ОС.
func detectDriver(ip, preferred string) printer.Printer {
	return newDetectingPrinter(creality.New("http://"+ip), nebulaos.New(ip), preferred)
}

// detectingPrinter — printer.Printer поверх нескольких драйверов: Info
// (первый вызов слоя приложения при подключении) зондирует драйверы по
// очереди, запомнил ответивший — все остальные методы делегируют ему.
type detectingPrinter struct {
	mu         sync.Mutex
	active     printer.Printer
	candidates []driverCandidate // в порядке зондирования
}

var _ printer.Printer = (*detectingPrinter)(nil)

// driverCandidate — один драйвер с его именем (для порядка зондов и
// сообщений об ошибках).
type driverCandidate struct {
	name   string
	client printer.Printer
}

func newDetectingPrinter(c *creality.Client, n *nebulaos.Client, preferred string) *detectingPrinter {
	candidates := []driverCandidate{
		{DriverCreality, c},
		{DriverNebulaOS, n},
	}
	if preferred == DriverNebulaOS {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
	return &detectingPrinter{candidates: candidates}
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

// Info зондирует драйверы по очереди (или проверяет уже активный) и
// запоминает ответивший.
func (d *detectingPrinter) Info(ctx context.Context) (printer.Info, error) {
	if c, err := d.driver(); err == nil {
		return c.Info(ctx)
	}
	var errs []string
	for _, cand := range d.candidates {
		info, err := cand.client.Info(ctx)
		if err != nil {
			errs = append(errs, cand.name+": "+err.Error())
			continue
		}
		d.setActive(cand.client)
		return info, nil
	}
	return printer.Info{}, fmt.Errorf("не удалось определить драйвер принтера: %s", strings.Join(errs, "; "))
}

// DriverName — имя выбранного драйвера (""): Info ещё не вызывался.
func (d *detectingPrinter) DriverName() string {
	c, err := d.driver()
	if err != nil {
		return ""
	}
	return c.DriverName()
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
