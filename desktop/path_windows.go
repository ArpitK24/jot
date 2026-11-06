//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const envKey = `Environment`

// userPath is the user and machine PATH from the registry, which is what
// a terminal opened after this point will see.
func userPath() string {
	var parts []string
	if k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("Path"); err == nil {
			parts = append(parts, v)
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("Path"); err == nil {
			parts = append(parts, v)
		}
		k.Close()
	}
	if len(parts) == 0 {
		return os.Getenv("PATH")
	}
	return strings.Join(parts, ";")
}

// addToPath appends dir to the user PATH and tells Windows it changed.
func addToPath(dir string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, d := range strings.Split(cur, ";") {
		if strings.EqualFold(strings.TrimRight(d, `\`), strings.TrimRight(dir, `\`)) {
			return nil
		}
	}
	next := dir
	if strings.TrimSpace(cur) != "" {
		next = strings.TrimRight(cur, ";") + ";" + dir
	}
	if err := k.SetExpandStringValue("Path", next); err != nil {
		return err
	}
	broadcastEnvChange()
	return nil
}

func broadcastEnvChange() {
	user32 := syscall.NewLazyDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, _ := syscall.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var result uintptr
	send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
