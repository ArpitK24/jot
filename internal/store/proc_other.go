//go:build !windows

package store

import "os/exec"

func hideWindow(*exec.Cmd) {}
