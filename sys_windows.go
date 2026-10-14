//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps ffmpeg/ffprobe from flashing a console window when the server runs on the desktop.
func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
