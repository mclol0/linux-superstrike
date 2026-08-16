package ui

import (
	"os"
	"os/exec"
	"syscall"
)

// restartApp relaunches this binary with the same arguments.
//
// It exists because some settings cannot be applied in place: Fyne latches
// systrayRunning on the first SetSystemTrayMenu call and offers no teardown, so
// an indicator that is already showing cannot be removed. Rather than telling
// the user to quit and start again by hand, we do it for them.
//
// syscall.Exec is preferred: it replaces the process image, so there is no
// window in which two instances fight over the same StatusNotifierItem name.
// If that fails (it should not on Linux), fall back to spawning a replacement
// and exiting.
func restartApp() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{exe}, os.Args[1:]...)

	if err := syscall.Exec(exe, args, os.Environ()); err != nil {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Env = os.Environ()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if serr := cmd.Start(); serr != nil {
			return serr
		}
		os.Exit(0)
	}
	return nil // unreachable: Exec either replaced us or returned an error
}
