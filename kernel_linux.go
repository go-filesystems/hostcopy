// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

package hostcopy

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"

	filesystem "github.com/go-filesystems/interface"
)

// copyFileRange is the system call, a variable so that a test can make the
// kernel decline.
var copyFileRange = unix.CopyFileRange

// kernelCopy copies with copy_file_range(2) until n bytes are copied, the
// source ends (eof), or the kernel declines. A refusal the kernel uses to say
// "not here" -- the ones Go's own os.File.ReadFrom falls back on -- returns
// what was copied and no error, for the caller to finish another way.
func kernelCopy(dst, src filesystem.HostFile, dstOff, srcOff, n int64) (done int64, eof bool, err error) {
	dc, err := dst.SyscallConn()
	if err != nil {
		return 0, false, nil
	}
	sc, err := src.SyscallConn()
	if err != nil {
		return 0, false, nil
	}
	var callErr error
	// Control, not Read or Write: regular files are not polled, and the
	// offsets are explicit, so neither descriptor's position moves. A
	// Control that fails -- a descriptor already closed -- copies nothing,
	// and the fallback's ReadAt or WriteAt then says what is wrong.
	_ = dc.Control(func(dfd uintptr) {
		_ = sc.Control(func(sfd uintptr) {
			for done < n {
				roff, woff := srcOff+done, dstOff+done
				k, err := copyFileRange(int(sfd), &roff, int(dfd), &woff, int(min(n-done, 1<<30)), 0)
				if err == syscall.EINTR {
					continue
				}
				if err != nil {
					if !declined(err) {
						callErr = err
					}
					return
				}
				if k == 0 {
					eof = true
					return
				}
				done += int64(k)
			}
		})
	})
	return done, eof, callErr
}

// declined reports whether err is the kernel saying it will not copy this
// pair, rather than that copying failed: the list Go's os.File.ReadFrom
// falls back on (internal/poll/copy_file_range_linux.go).
func declined(err error) bool {
	for _, e := range []error{unix.ENOSYS, unix.EXDEV, unix.EINVAL, unix.EIO, unix.EOPNOTSUPP, unix.EPERM} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
