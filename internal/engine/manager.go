package engine

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
)

// Manager runs Dozzle as a child process and restarts it when settings change.
type Manager struct {
	Binary  string
	DataDir string
	Addr    string // e.g. ":8081" — Dozzle listen address (proxied by control plane)

	mu  sync.Mutex
	cmd *exec.Cmd
}

func New(binary, dataDir, addr string) *Manager {
	if addr == "" {
		addr = ":8081"
	}
	return &Manager{Binary: binary, DataDir: dataDir, Addr: addr}
}

func (m *Manager) Apply(cfg config.Settings, namespaces []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.killLocked(); err != nil {
		log.Printf("engine: stop previous: %v", err)
	}

	if len(namespaces) == 0 {
		return fmt.Errorf("no namespaces selected — open /admin and choose namespaces")
	}

	env := os.Environ()
	env = setEnv(env, "DOZZLE_MODE", "k8s")
	env = setEnv(env, "DOZZLE_NAMESPACE", strings.Join(namespaces, ","))
	env = setEnv(env, "DOZZLE_HOSTNAME", cfg.Title)
	env = setEnv(env, "DOZZLE_NO_ANALYTICS", "true")
	if cfg.Filter != "" {
		env = setEnv(env, "DOZZLE_FILTER", cfg.Filter)
	} else {
		env = unsetEnv(env, "DOZZLE_FILTER")
	}

	env = setEnv(env, "DOZZLE_ADDR", m.Addr)

	cmd := exec.Command(m.Binary, "--addr", m.Addr)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if m.DataDir != "" {
		cmd.Dir = m.DataDir
	}
	configureProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dozzle: %w", err)
	}
	m.cmd = cmd
	log.Printf("engine: dozzle started pid=%d namespaces=%s", cmd.Process.Pid, strings.Join(namespaces, ","))

	go func(c *exec.Cmd) {
		if err := c.Wait(); err != nil {
			log.Printf("engine: dozzle exited: %v", err)
		}
	}(cmd)

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.killLocked()
}

func (m *Manager) killLocked() error {
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	pid := m.cmd.Process.Pid
	_ = terminateProcess(pid)
	done := make(chan struct{})
	go func() {
		_, _ = m.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = forceKillProcess(pid)
	}
	m.cmd = nil
	return nil
}

func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}
