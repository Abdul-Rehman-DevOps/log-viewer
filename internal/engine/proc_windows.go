//go:build windows

package engine

import (
	"os"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd) {}

func terminateProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func forceKillProcess(pid int) error {
	return terminateProcess(pid)
}
