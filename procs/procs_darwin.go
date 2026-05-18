//go:build darwin

package procs

import (
	"context"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin proc_info syscall arguments. See xnu's bsd/sys/proc_info.h.
const (
	// callnum values for SYS_PROC_INFO.
	procInfoCallListPIDs = 1

	// proc_listpids "type" argument.
	procAllPIDs = 1
)

// systemEnumerate walks the live process table via the proc_info
// syscall plus KERN_PROCARGS2 for argv. Per-pid errors are swallowed
// so a single transient pid (exited between listing and reading) does
// not abort the walk.
func systemEnumerate(ctx context.Context) ([]Process, error) {
	pids, err := listAllPIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid == 0 {
			continue
		}
		cwd, ok := readCWD(pid)
		if !ok {
			continue
		}
		args := readArgv(pid)
		out = append(out, Process{
			Pid:     int(pid),
			Cwd:     cwd,
			Command: commandFromArgs(args),
			Args:    args,
		})
	}
	return out, nil
}

// listAllPIDs returns every visible pid using proc_listpids with
// PROC_ALL_PIDS. Performs a size-probe call first, then a sized read.
// We double the probe result to absorb forks between calls.
func listAllPIDs() ([]int32, error) {
	probe, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallListPIDs,
		procAllPIDs,
		0,
		0,
		0, 0,
	)
	if errno != 0 {
		return nil, errno
	}
	if probe == 0 {
		return nil, nil
	}

	bufBytes := int(probe) * 2
	buf := make([]byte, bufBytes)
	n, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallListPIDs,
		procAllPIDs,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(bufBytes),
		0,
	)
	if errno != 0 {
		return nil, errno
	}

	const sz = int(unsafe.Sizeof(int32(0)))
	count := int(n) / sz
	pids := make([]int32, count)
	for i := range pids {
		pids[i] = *(*int32)(unsafe.Pointer(&buf[i*sz]))
	}
	return pids, nil
}

// readCWD is implemented in Task 4.
func readCWD(pid int32) (string, bool) {
	_ = pid
	return "", false
}

// readArgv is implemented in Task 5.
func readArgv(pid int32) []string {
	_ = pid
	return nil
}

// commandFromArgs returns Args[0] basename, or "" if Args is empty.
func commandFromArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	a := args[0]
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == '/' {
			return a[i+1:]
		}
	}
	return a
}

// enumerate is the seam picked up by ListByCwdPrefixes via the
// package-level `enumerator` var.
func enumerate(ctx context.Context) ([]Process, error) {
	return systemEnumerate(ctx)
}
