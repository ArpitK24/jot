//go:build !windows

package main

import (
	"errors"
	"os"
)

// userPath is the PATH a new terminal will see.
func userPath() string { return os.Getenv("PATH") }

func addToPath(string) error {
	return errors.New("add the jot folder to PATH in your shell profile")
}
