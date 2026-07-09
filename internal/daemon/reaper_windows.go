//go:build windows

package daemon

import "syscall"

const detachedProcess = 0x00000008

func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess}
}
