// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

package hostcopy

import (
	"errors"
	"io"

	filesystem "github.com/go-filesystems/interface"
)

// ErrNegative is returned for a negative offset or length.
var ErrNegative = errors.New("hostcopy: negative offset or length")

// chunk is what the fallback reads and writes at a time.
const chunk = 1 << 20

// Range copies up to n bytes of src, starting at srcOff, into dst at dstOff,
// and returns how many it copied. It stops early, without an error, at the
// end of src. dst's Size accounts for the bytes written when Range returns.
func Range(dst filesystem.WritableFile, src filesystem.File, dstOff, srcOff, n int64) (int64, error) {
	if dstOff < 0 || srcOff < 0 || n < 0 {
		return 0, ErrNegative
	}
	var done int64
	var eof bool
	if hd, ok := dst.(filesystem.HostFile); ok {
		if hs, ok := src.(filesystem.HostFile); ok {
			k, end, err := kernelCopy(hd, hs, dstOff, srcOff, n)
			done, eof = k, end
			if err != nil {
				return done, err
			}
			// The kernel wrote behind the driver's back: a WritableFile's
			// Size follows its own writes and truncations, so it is told
			// the new end the only way the interface has. On the host the
			// file is already that long, and nothing changes there.
			if end := dstOff + done; done > 0 && end > dst.Size() {
				if err := dst.Truncate(end); err != nil {
					return done, err
				}
			}
		}
	}
	if done == n || eof {
		return done, nil
	}
	k, err := byReadAt(dst, src, dstOff+done, srcOff+done, n-done)
	return done + k, err
}

// byReadAt copies with ReadAt and WriteAt, one chunk at a time.
func byReadAt(dst filesystem.WritableFile, src filesystem.File, dstOff, srcOff, n int64) (int64, error) {
	buf := make([]byte, min(n, chunk))
	var done int64
	for done < n {
		p := buf[:min(int64(len(buf)), n-done)]
		r, err := src.ReadAt(p, srcOff+done)
		if r > 0 {
			if _, werr := dst.WriteAt(p[:r], dstOff+done); werr != nil {
				return done, werr
			}
			done += int64(r)
		}
		if errors.Is(err, io.EOF) {
			return done, nil
		}
		if err != nil {
			return done, err
		}
	}
	return done, nil
}
