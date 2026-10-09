// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

package hostcopy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"

	filesystem "github.com/go-filesystems/interface"
)

// memFile is a File and a WritableFile over a byte slice, counting the
// calls that copy through this process.
type memFile struct {
	data           []byte
	reads, writes  atomic.Int64
	readErr, wrErr error
}

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	m.reads.Add(1)
	if m.readErr != nil {
		return 0, m.readErr
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	m.writes.Add(1)
	if m.wrErr != nil {
		return 0, m.wrErr
	}
	if end := off + int64(len(p)); end > int64(len(m.data)) {
		m.data = append(m.data, make([]byte, end-int64(len(m.data)))...)
	}
	return copy(m.data[off:], p), nil
}

func (m *memFile) Size() int64  { return int64(len(m.data)) }
func (m *memFile) Close() error { return nil }
func (m *memFile) Sync() error  { return nil }
func (m *memFile) Truncate(n int64) error {
	m.data = append(m.data[:0:0], m.data[:min(n, int64(len(m.data)))]...)
	return nil
}

// hostFile is a HostFile over a real file whose Size is cached and follows
// only its own writes and truncations, as go-filesystems/osfs's does.
type hostFile struct {
	*os.File
	size          atomic.Int64
	reads, writes atomic.Int64
	// connErr and truncErr make SyscallConn and Truncate fail.
	connErr, truncErr error
}

func (h *hostFile) SyscallConn() (syscall.RawConn, error) {
	if h.connErr != nil {
		return nil, h.connErr
	}
	return h.File.SyscallConn()
}

func (h *hostFile) ReadAt(p []byte, off int64) (int, error) {
	h.reads.Add(1)
	return h.File.ReadAt(p, off)
}

func (h *hostFile) WriteAt(p []byte, off int64) (int, error) {
	h.writes.Add(1)
	n, err := h.File.WriteAt(p, off)
	if end := off + int64(n); end > h.size.Load() {
		h.size.Store(end)
	}
	return n, err
}

func (h *hostFile) Size() int64 { return h.size.Load() }
func (h *hostFile) Truncate(n int64) error {
	if h.truncErr != nil {
		return h.truncErr
	}
	if err := h.File.Truncate(n); err != nil {
		return err
	}
	h.size.Store(n)
	return nil
}
func (*hostFile) HostFile() {}

var (
	_ filesystem.WritableFile = (*memFile)(nil)
	_ filesystem.HostFile     = (*hostFile)(nil)
	_ filesystem.WritableFile = (*hostFile)(nil)
)

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>11)
	}
	return b
}

func newHostFile(t *testing.T, name string, data []byte) *hostFile {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	h := &hostFile{File: f}
	h.size.Store(int64(len(data)))
	return h
}

func TestRangeByReadAt(t *testing.T) {
	for _, n := range []int{0, 1, chunk - 1, chunk, chunk + 1, 3*chunk + 5} {
		src := &memFile{data: pattern(n)}
		dst := &memFile{data: []byte("prefix")}
		got, err := Range(dst, src, 3, 0, int64(n))
		if err != nil || got != int64(n) {
			t.Fatalf("n=%d: copied %d, %v", n, got, err)
		}
		if want := append([]byte("pre"), pattern(n)...); n > 3 && !bytes.Equal(dst.data, want) {
			t.Fatalf("n=%d: destination differs", n)
		}
	}
}

// A length past the end of the source stops there, without an error.
func TestRangeStopsAtTheEndOfTheSource(t *testing.T) {
	src := &memFile{data: pattern(chunk + 10)}
	dst := &memFile{}
	got, err := Range(dst, src, 0, 5, 10*chunk)
	if err != nil || got != chunk+5 || !bytes.Equal(dst.data, src.data[5:]) {
		t.Fatalf("copied %d (%v), want %d", got, err, chunk+5)
	}
}

func TestRangeRefusesNegatives(t *testing.T) {
	for _, a := range [][3]int64{{-1, 0, 1}, {0, -1, 1}, {0, 0, -1}} {
		if _, err := Range(&memFile{}, &memFile{}, a[0], a[1], a[2]); !errors.Is(err, ErrNegative) {
			t.Fatalf("Range%v = %v, want ErrNegative", a, err)
		}
	}
}

func TestRangePassesErrorsOn(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Range(&memFile{}, &memFile{data: pattern(10), readErr: boom}, 0, 0, 10); !errors.Is(err, boom) {
		t.Fatalf("read error: %v", err)
	}
	if _, err := Range(&memFile{wrErr: boom}, &memFile{data: pattern(10)}, 0, 0, 10); !errors.Is(err, boom) {
		t.Fatalf("write error: %v", err)
	}
}

// Two files of the host: on Linux the kernel copies and nothing passes
// through ReadAt or WriteAt; elsewhere they do. Either way the bytes and the
// destination's Size are right.
func TestRangeBetweenHostFiles(t *testing.T) {
	data := pattern(5*chunk + 123)
	src := newHostFile(t, "src", data)
	dst := newHostFile(t, "dst", []byte("0123456789"))
	got, err := Range(dst, src, 4, 100, int64(len(data)))
	if err != nil || got != int64(len(data)-100) {
		t.Fatalf("copied %d, %v", got, err)
	}
	if dst.Size() != 4+int64(len(data)-100) {
		t.Fatalf("destination Size %d, want %d", dst.Size(), 4+len(data)-100)
	}
	on, _ := os.ReadFile(dst.Name())
	if !bytes.Equal(on, append([]byte("0123"), data[100:]...)) {
		t.Fatal("destination bytes differ")
	}
	through := src.reads.Load() + dst.writes.Load()
	if runtime.GOOS == "linux" && through != 0 {
		t.Fatalf("%d ReadAt/WriteAt calls: the kernel did not copy", through)
	}
	if runtime.GOOS != "linux" && through == 0 {
		t.Fatal("nothing went through ReadAt/WriteAt, and there is no kernel copy here")
	}
}

// A HostFile whose descriptor is gone copies nothing in the kernel, and the
// fallback says why.
func TestRangeWithAClosedHostFile(t *testing.T) {
	src := newHostFile(t, "src", pattern(100))
	dst := newHostFile(t, "dst", nil)
	src.File.Close()
	if _, err := Range(dst, src, 0, 0, 100); err == nil || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("copy from a closed file: %v, want os.ErrClosed", err)
	}
}

// A HostFile that cannot give its descriptor is copied the other way.
func TestRangeWithoutADescriptor(t *testing.T) {
	no := errors.New("no descriptor")
	for _, side := range []string{"dst", "src"} {
		data := pattern(chunk + 3)
		src := newHostFile(t, "src", data)
		dst := newHostFile(t, "dst", nil)
		if side == "dst" {
			dst.connErr = no
		} else {
			src.connErr = no
		}
		got, err := Range(dst, src, 0, 0, int64(len(data)))
		if err != nil || got != int64(len(data)) || src.reads.Load() == 0 {
			t.Fatalf("%s without a descriptor: copied %d, %v, %d ReadAt", side, got, err, src.reads.Load())
		}
	}
}
