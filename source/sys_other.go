//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

var errWinOnly = errors.New("only available on Windows")

func installTask(exe string) error         { return errWinOnly }
func installTaskElevated(exe string) error { return errWinOnly }
func uninstallTask() error                 { return errWinOnly }
func startTask() error                     { return errWinOnly }
func killOthers() error                    { return errWinOnly }
func hasConsole() bool                     { return true }
