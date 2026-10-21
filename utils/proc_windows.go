//go:build windows

package utils

import "os/exec"

// DMP 只在 Linux/macOS 上跑游戏，这两个函数是为了在 Windows 上也能编译。
// Windows 没有进程组，只能杀直接子进程。

func setupProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
