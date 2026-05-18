//go:build darwin

package procs

import (
	"bytes"
	"context"
	"path/filepath"
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
	procsList, err := listAllProcs()
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(procsList))
	for _, kp := range procsList {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid := kp.Proc.P_pid
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

// listAllProcs returns every visible process entry via sysctl kern.proc.all.
// This is the documented public API and works for unsigned binaries
// on macOS 26+ (Tahoe), where the proc_info listpids path is
// restricted without an entitlement.
func listAllProcs() ([]unix.KinfoProc, error) {
	return unix.SysctlKinfoProcSlice("kern.proc.all")
}

// procVNodePathInfoSize is sizeof(struct proc_vnodepathinfo) from
// xnu's bsd/sys/proc_info.h. Two proc_vnodeinfo_path entries (cdir,
// rdir), each containing a proc_vnodeinfo (152 bytes) and a
// proc_vnodepath (1024 bytes path + flags). Total = 2 * 1176 = 2352.
// The kernel requires the full struct size; a shorter buffer causes the
// syscall to fail. Verified against xnu-* headers.
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
	end := bytes.IndexByte(path, 0)
	switch end {
	case -1:
		return string(path), true
	case 0:
		return "", false
	default:
		return string(path[:end]), true
	}
}

// nextCString returns the next null-terminated string from b along with
// the remaining slice after the null. ok is false if no null was found.
func nextCString(b []byte) (s string, rest []byte, ok bool) {
	end := bytes.IndexByte(b, 0)
	if end < 0 {
		return "", nil, false
	}
	return string(b[:end]), b[end+1:], true
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

	p := buf[4:]

	_, rest, ok := nextCString(p)
	if !ok {
		return nil
	}
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	p = rest

	args := make([]string, 0, argc)
	for len(args) < int(argc) && len(p) > 0 {
		s, rest, ok := nextCString(p)
		if !ok {
			args = append(args, string(p))
			break
		}
		args = append(args, s)
		p = rest
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

// commandFromArgs returns Args[0] basename, or "" if Args is empty or
// args[0] is empty.
func commandFromArgs(args []string) string {
	if len(args) == 0 || args[0] == "" {
		return ""
	}
	return filepath.Base(args[0])
}

// enumerate is the seam picked up by ListByCwdPrefixes via the
// package-level `enumerator` var.
func enumerate(ctx context.Context) ([]Process, error) {
	return systemEnumerate(ctx)
}
