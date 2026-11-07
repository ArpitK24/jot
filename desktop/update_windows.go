//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// runInstaller starts the Inno Setup installer silently, detached from
// jot, so it can replace jot's files once the window quits. /RELAUNCH
// tells it to open jot again when it's done.
func runInstaller(path string) error {
	cmd := exec.Command(path, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/CURRENTUSER", "/RELAUNCH")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
