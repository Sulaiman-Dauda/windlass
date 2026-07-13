// Package plugins runs optional extensions as external processes. A plugin
// is a directory under DATA_DIR/plugins/<name>/ containing plugin.json and
// an executable. Disabled plugins are simply not running — zero RAM, zero
// goroutines (principle: everything optional costs nothing when off).
//
// Contract: Windlass starts the executable with WINDLASS_PLUGIN_ADDR set to
// a localhost address the plugin must listen on. All HTTP traffic under
// /api/v1/plugins/<name>/proxy/* is reverse-proxied to it.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/windlass-dev/windlass/internal/store/db"
)

var ErrNotFound = errors.New("plugin not found")

type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Command is the executable, relative to the plugin directory.
	Command string `json:"command"`
	// UI marks plugins that serve a web UI at their root.
	UI bool `json:"ui,omitempty"`
}

type Plugin struct {
	Manifest
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
}

type instance struct {
	cmd    *exec.Cmd
	addr   string
	proxy  *httputil.ReverseProxy
	cancel context.CancelFunc
}

type Service struct {
	q      *db.Queries
	dir    string // plugins root
	logger *slog.Logger

	mu      sync.Mutex
	running map[string]*instance
}

func New(q *db.Queries, dataDir string, logger *slog.Logger) *Service {
	return &Service{
		q:       q,
		dir:     filepath.Join(dataDir, "plugins"),
		logger:  logger,
		running: map[string]*instance{},
	}
}

// discover reads manifests from the plugins directory.
func (s *Service) discover() (map[string]Manifest, error) {
	out := map[string]Manifest{}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "plugin.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil || m.Name != e.Name() || m.Command == "" {
			s.logger.Warn("invalid plugin manifest", "dir", e.Name())
			continue
		}
		if strings.Contains(m.Command, "..") || filepath.IsAbs(m.Command) ||
			strings.HasPrefix(m.Command, "/") || filepath.VolumeName(m.Command) != "" {
			s.logger.Warn("plugin command must be relative to its directory", "plugin", m.Name)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

func (s *Service) List(ctx context.Context) ([]Plugin, error) {
	manifests, err := s.discover()
	if err != nil {
		return nil, err
	}
	enabled, err := s.q.ListEnabledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	enabledSet := map[string]bool{}
	for _, name := range enabled {
		enabledSet[name] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Plugin, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, Plugin{
			Manifest: m,
			Enabled:  enabledSet[m.Name],
			Running:  s.running[m.Name] != nil,
		})
	}
	return out, nil
}

func (s *Service) Enable(ctx context.Context, name string) error {
	manifests, err := s.discover()
	if err != nil {
		return err
	}
	m, ok := manifests[name]
	if !ok {
		return ErrNotFound
	}
	if err := s.q.SetPluginEnabled(ctx, db.SetPluginEnabledParams{Name: name, Enabled: 1}); err != nil {
		return err
	}
	return s.start(m)
}

func (s *Service) Disable(ctx context.Context, name string) error {
	if err := s.q.SetPluginEnabled(ctx, db.SetPluginEnabledParams{Name: name, Enabled: 0}); err != nil {
		return err
	}
	s.stop(name)
	return nil
}

// StartEnabled launches every enabled plugin; called at boot.
func (s *Service) StartEnabled(ctx context.Context) {
	manifests, err := s.discover()
	if err != nil {
		s.logger.Error("plugin discovery", "error", err)
		return
	}
	enabled, err := s.q.ListEnabledPlugins(ctx)
	if err != nil {
		return
	}
	for _, name := range enabled {
		if m, ok := manifests[name]; ok {
			if err := s.start(m); err != nil {
				s.logger.Error("plugin start", "plugin", name, "error", err)
			}
		}
	}
}

func (s *Service) start(m Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[m.Name] != nil {
		return nil
	}

	// Reserve a localhost port for the plugin.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	bin := filepath.Join(s.dir, m.Name, filepath.FromSlash(m.Command))
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = filepath.Join(s.dir, m.Name)
	cmd.Env = append(os.Environ(), "WINDLASS_PLUGIN_ADDR="+addr)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start plugin: %w", err)
	}

	target, _ := url.Parse("http://" + addr)
	inst := &instance{cmd: cmd, addr: addr, cancel: cancel,
		proxy: httputil.NewSingleHostReverseProxy(target)}
	s.running[m.Name] = inst
	s.logger.Info("plugin started", "plugin", m.Name, "addr", addr)

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.running[m.Name] == inst {
			delete(s.running, m.Name)
		}
		s.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			s.logger.Error("plugin exited", "plugin", m.Name, "error", err)
		}
	}()
	return nil
}

func (s *Service) stop(name string) {
	s.mu.Lock()
	inst := s.running[name]
	delete(s.running, name)
	s.mu.Unlock()
	if inst == nil {
		return
	}
	inst.cancel()
	// cmd.Wait in the start goroutine reaps the process.
	s.logger.Info("plugin stopped", "plugin", name)
}

// StopAll terminates running plugins (shutdown path).
func (s *Service) StopAll() {
	s.mu.Lock()
	names := make([]string, 0, len(s.running))
	for name := range s.running {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.stop(name)
	}
}

// Proxy forwards a request to a running plugin, or 503s if it is not up.
func (s *Service) Proxy(name string, w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	inst := s.running[name]
	s.mu.Unlock()
	if inst == nil {
		http.Error(w, `{"error":{"code":"plugin_unavailable","message":"plugin is not running"}}`,
			http.StatusServiceUnavailable)
		return
	}
	// Give slow plugin starts a moment on the very first request.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", inst.addr, 250*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			break // let the proxy surface the error
		}
	}
	inst.proxy.ServeHTTP(w, r)
}
