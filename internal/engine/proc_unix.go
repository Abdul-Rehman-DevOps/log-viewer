//go:build unix

package engine

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcess(pid int) error {
	if pgid, err := syscall.Getpgid(pid); err == nil {
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

func forceKillProcess(pid int) error {
	if pgid, err := syscall.Getpgid(pid); err == nil {
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
