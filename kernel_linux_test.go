// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

package hostcopy

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// withCopyFileRange replaces the system call for one test.
func withCopyFileRange(t *testing.T, f func(rfd int, roff *int64, wfd int, woff *int64, n int, flags int) (int, error)) {
	t.Helper()
	real := copyFileRange
	copyFileRange = f
	t.Cleanup(func() { copyFileRange = real })
}

// The kernel declining -- across filesystems, say -- is not an error: the
// copy goes on through ReadAt and WriteAt, from where the kernel stopped.
func TestAKernelThatDeclinesHandsOver(t *testing.T) {
	for _, before := range []int{0, 4096} {
		calls := 0
		withCopyFileRange(t, func(rfd int, roff *int64, wfd int, woff *int64, n int, flags int) (int, error) {
			calls++
			if calls == 1 && before > 0 {
				return unix.CopyFileRange(rfd, roff, wfd, woff, before, flags)
			}
			return 0, unix.EXDEV
		})
		data := pattern(3*chunk + 9)
		src := newHostFile(t, "src", data)
		dst := newHostFile(t, "dst", nil)
		got, err := Range(dst, src, 0, 0, int64(len(data)))
		if err != nil || got != int64(len(data)) {
			t.Fatalf("before=%d: copied %d, %v", before, got, err)
		}
		on, _ := os.ReadFile(dst.Name())
		if !bytes.Equal(on, data) || dst.Size() != int64(len(data)) {
			t.Fatalf("before=%d: %d bytes on disk, Size %d", before, len(on), dst.Size())
		}
		if src.reads.Load() == 0 {
			t.Fatalf("before=%d: the fallback never ran", before)
		}
	}
}

// A real failure is not a refusal, and comes back as one.
func TestAKernelThatFailsSaysSo(t *testing.T) {
	withCopyFileRange(t, func(int, *int64, int, *int64, int, int) (int, error) { return 0, unix.ENOSPC })
	src := newHostFile(t, "src", pattern(100))
	dst := newHostFile(t, "dst", nil)
	if _, err := Range(dst, src, 0, 0, 100); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("Range = %v, want ENOSPC", err)
	}
}

// An interrupted call is made again.
func TestAnInterruptedKernelCopyIsRetried(t *testing.T) {
	calls := 0
	withCopyFileRange(t, func(rfd int, roff *int64, wfd int, woff *int64, n int, flags int) (int, error) {
		calls++
		if calls == 1 {
			return 0, unix.EINTR
		}
		return unix.CopyFileRange(rfd, roff, wfd, woff, n, flags)
	})
	data := pattern(chunk)
	src := newHostFile(t, "src", data)
	dst := newHostFile(t, "dst", nil)
	if got, err := Range(dst, src, 0, 0, int64(len(data))); err != nil || got != int64(len(data)) || src.reads.Load() != 0 {
		t.Fatalf("copied %d, %v, %d ReadAt calls", got, err, src.reads.Load())
	}
}

func TestDeclined(t *testing.T) {
	for _, e := range []error{unix.ENOSYS, unix.EXDEV, unix.EINVAL, unix.EIO, unix.EOPNOTSUPP, unix.EPERM} {
		if !declined(e) {
			t.Errorf("%v is not read as a refusal", e)
		}
	}
	if declined(unix.ENOSPC) {
		t.Error("ENOSPC is read as a refusal")
	}
}

// The destination that cannot be told its new size makes the copy fail: a
// Size that lies is worse than an error.
func TestATruncateThatFailsAfterAKernelCopy(t *testing.T) {
	boom := errors.New("truncate")
	src := newHostFile(t, "src", pattern(100))
	dst := newHostFile(t, "dst", nil)
	dst.truncErr = boom
	if got, err := Range(dst, src, 0, 0, 100); !errors.Is(err, boom) || got != 100 {
		t.Fatalf("Range = %d, %v; want 100 and the truncate error", got, err)
	}
}
