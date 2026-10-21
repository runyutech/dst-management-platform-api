//go:build !windows

package utils

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup 让子进程自己成一个进程组。
// steamcmd.sh 会再拉起 linux32/steamcmd 子进程，只杀 bash 的话子进程会继续占着工坊目录，
// 下一次下载就会一直等，所以超时后必须按进程组杀。
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup 杀掉整个进程组
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// 负 pid 表示整个进程组
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
