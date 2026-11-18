package config

import (
	"os"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if c.LastIP != "" {
		t.Errorf("LastIP = %q, want empty", c.LastIP)
	}
	if c.TotalBytes != 0 {
		t.Errorf("TotalBytes = %d, want 0 (дефолт вычисляется)", c.TotalBytes)
	}
	if c.Total() != DefaultTotalBytes {
		t.Errorf("Total() = %d, want %d", c.Total(), DefaultTotalBytes)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.LastIP = "192.168.3.166"
	c.TotalBytes = 4 * 1024 * 1024 * 1024
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if c2.LastIP != "192.168.3.166" {
		t.Errorf("LastIP = %q, want 192.168.3.166", c2.LastIP)
	}
	if c2.TotalBytes != 4*1024*1024*1024 {
		t.Errorf("TotalBytes = %d, want 4 GiB", c2.TotalBytes)
	}
	if c2.Total() != 4*1024*1024*1024 {
		t.Errorf("Total() = %d, want явный TotalBytes", c2.Total())
	}
}

func TestSaveCreatesDirIfNeeded(t *testing.T) {
	dir := t.TempDir() + "/nested/config"
	c := &Config{LastIP: "10.0.0.5"}
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save в несуществующий каталог: %v", err)
	}
	if _, err := os.Stat(dir + "/" + Filename); err != nil {
		t.Errorf("файл конфига не создан: %v", err)
	}
}

func TestLoadIgnoresCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+Filename, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("повреждённый конфиг должен игнорироваться, не ошибаться: %v", err)
	}
	if c.LastIP != "" {
		t.Errorf("LastIP = %q, want дефолт", c.LastIP)
	}
}
