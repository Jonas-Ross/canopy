//go:build darwin

package procs

import (
	"context"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin proc_info syscall arguments. See xnu's bsd/sys/proc_info.h.
const (
	// callnum value for SYS_PROC_INFO.
	procInfoCallPIDInfo = 2

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

// listAllPIDs returns every visible pid via sysctl kern.proc.all.
// This is the documented public API and works for unsigned binaries
// on macOS 26+ (Tahoe), where the proc_info listpids path is
// restricted without an entitlement.
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
	// n == procVNodePathInfoSize on success; we only need cdir (first half).
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

// readArgv returns the argv vector of a pid via sysctl KERN_PROCARGS2.
// The kernel returns a packed buffer:
//
//	int32 argc
//	char  exec_path[]   // null-terminated, zero-padded
//	char  argv[argc][]  // each null-terminated
//	char  envp[]        // ignored
//
// Returns nil on any sysctl error (transient pid, permission denied,
// short buffer). nil is the same shape Linux returns for kernel
// threads, so callers treat both identically.
func readArgv(pid int32) []string {
	buf, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil || len(buf) < 4 {
		return nil
	}

	argc := *(*int32)(unsafe.Pointer(&buf[0]))
	if argc <= 0 {
		return nil
	}

	// Skip the int32 argc header.
	p := buf[4:]

	// Skip exec_path: the first null-terminated string.
	i := 0
	for i < len(p) && p[i] != 0 {
		i++
	}
	if i >= len(p) {
		return nil
	}
	// Skip the null terminator AND any zero padding that follows.
	for i < len(p) && p[i] == 0 {
		i++
	}
	p = p[i:]

	// Read argc null-terminated strings.
	args := make([]string, 0, argc)
	for len(args) < int(argc) && len(p) > 0 {
		end := 0
		for end < len(p) && p[end] != 0 {
			end++
		}
		args = append(args, string(p[:end]))
		if end >= len(p) {
			break
		}
		p = p[end+1:]
	}
	if len(args) == 0 {
		return nil
	}
	return args
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
