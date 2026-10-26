//go:build windows

package main

// Single-instance guard via a named mutex.

import (
	"syscall"
	"unsafe"
)

var (
	kernel32SI       = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex  = kernel32SI.NewProc("CreateMutexW")
	procReleaseMutex = kernel32SI.NewProc("ReleaseMutex")
)

const (
	errorAlreadyExists = 183
)

var mutexHandle uintptr

// acquireSingleInstance returns false if another tray instance is running.
func acquireSingleInstance() bool {
	name, _ := syscall.UTF16PtrFromString("Global\\SDPToggleTraySingleInstance")
	h, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return true // couldn't create; allow anyway
	}
	mutexHandle = h
	return err != syscall.Errno(errorAlreadyExists)
}

func releaseSingleInstance() {
	if mutexHandle != 0 {
		procReleaseMutex.Call(mutexHandle)
		mutexHandle = 0
	}
}
