//go:build windows

package store

import (
	"os/exec"
	"syscall"
)

// createNoWindow stops git from flashing a console window when jot runs
// inside a GUI process such as the desktop app.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
