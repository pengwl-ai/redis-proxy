package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTemp(t, `
proxy:
  listen: "0.0.0.0:6379"
  api_listen: "0.0.0.0:8080"
backends:
  - name: "dc1"
    addr: "127.0.0.1:6379"
    role: primary
  - name: "dc2"
    addr: "127.0.0.1:6380"
    role: standby
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(cfg.Backends))
	}
	if cfg.Backends[0].Role != "primary" {
		t.Errorf("expected primary, got %s", cfg.Backends[0].Role)
	}
	if cfg.Backends[1].Role != "standby" {
		t.Errorf("expected standby, got %s", cfg.Backends[1].Role)
	}
}

func TestLoadDefaults(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: "127.0.0.1:6379"
    role: primary
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Listen != "0.0.0.0:6379" {
		t.Errorf("expected default listen, got %q", cfg.Proxy.Listen)
	}
	if cfg.Proxy.APIListen != "0.0.0.0:8080" {
		t.Errorf("expected default api_listen, got %q", cfg.Proxy.APIListen)
	}
}

func TestLoadMissingPrimary(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: "127.0.0.1:6379"
    role: standby
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing primary")
	}
}

func TestLoadInvalidRole(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: "127.0.0.1:6379"
    role: readonly
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestLoadEmptyName(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: ""
    addr: "127.0.0.1:6379"
    role: primary
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestLoadEmptyAddr(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: ""
    role: primary
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty addr")
	}
}

func TestLoadInvalidAddr(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: "not-a-valid-addr"
    role: primary
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid addr")
	}
}

func TestLoadDuplicateName(t *testing.T) {
	path := writeTemp(t, `
backends:
  - name: "dc1"
    addr: "127.0.0.1:6379"
    role: primary
  - name: "dc1"
    addr: "127.0.0.1:6380"
    role: standby
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestLoadNoBackends(t *testing.T) {
	path := writeTemp(t, `
proxy:
  listen: "0.0.0.0:6379"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for no backends")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
