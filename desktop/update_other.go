//go:build !windows

package main

func runInstaller(string) error { return errNoInstaller }
