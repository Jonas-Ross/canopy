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
	procInfoCallPIDInfo  = 2

	// proc_listpids "type" argument.
	procAllPIDs = 1

	// proc_pidinfo "flavor" values.
	procPIDVNodePathInfo = 9
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

// listAllPIDs returns every visible pid using sysctl kern.proc.all.
// proc_listpids (SYS_PROC_INFO callnum=1) is restricted on macOS 26+
// (Tahoe) when the binary is unsigned; sysctl kern.proc.all works for
// any process without special entitlements.
func listAllPIDs() ([]int32, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int32, 0, len(procs))
	for _, p := range procs {
		pids = append(pids, p.Proc.P_pid)
	}
	return pids, nil
}

// procVNodePathInfoSize is sizeof(struct proc_vnodepathinfo) from
// xnu's bsd/sys/proc_info.h. Two proc_vnodeinfo_path entries (cdir,
// rdir), each containing a proc_vnodeinfo (152 bytes) and a
// proc_vnodepath (1024 bytes path + flags). Total = 2 * 1176 = 2352.
// Verified against xnu-* headers; pinned here so a Go-side struct
// definition drift can't surface as a silent buffer overrun.
const procVNodePathInfoSize = 2352

// cdirPathOffset is the byte offset of cdir.vip_path inside the
// proc_vnodepathinfo struct. cdir is the first proc_vnodeinfo_path
// entry; vip_path is preceded by the 152-byte vip_vi (proc_vnodeinfo).
const cdirPathOffset = 152

// maxPathLen mirrors MAXPATHLEN (PATH_MAX) on darwin.
const maxPathLen = 1024

// readCWD returns the cwd for a pid via proc_pidinfo. Returns false on
// any syscall error (ESRCH if the pid exited, EPERM for processes
// outside the caller's uid). Errors are silent — a transient pid must
// not abort the walk.
func readCWD(pid int32) (string, bool) {
	var buf [procVNodePathInfoSize]byte
	n, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallPIDInfo,
		uintptr(pid),
		procPIDVNodePathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if errno != 0 {
		return "", false
	}
	if int(n) < cdirPathOffset+maxPathLen {
		return "", false
	}
	path := buf[cdirPathOffset : cdirPathOffset+maxPathLen]
	end := 0
	for end < len(path) && path[end] != 0 {
		end++
	}
	if end == 0 {
		return "", false
	}
	return string(path[:end]), true
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
