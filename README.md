# hostcopy

Copy a range of one [go-filesystems](https://github.com/go-filesystems) driver
file into another — without holding the file in memory, and without passing
it through the process at all when both are files of the host.

```go
n, err := hostcopy.Range(dst, src, dstOff, srcOff, length)
```

`dst` is a [`filesystem.WritableFile`](https://pkg.go.dev/github.com/go-filesystems/interface#WritableFile),
`src` a [`filesystem.File`](https://pkg.go.dev/github.com/go-filesystems/interface#File).
`Range` stops early, without an error, at the end of `src`, and `dst.Size()`
accounts for what it wrote.

It is what a file server's server-side copy is made of — WebDAV `COPY`, SFTP
`copy-data`, SMB `FSCTL_SRV_COPYCHUNK`, S3 `CopyObject` — where the obvious
alternative, `ReadFile` then `WriteFile`, allocates the size of the file being
copied.

## How it copies

- **Both files are [`filesystem.HostFile`](https://pkg.go.dev/github.com/go-filesystems/interface#HostFile)
  (as [`osfs`](https://github.com/go-filesystems/osfs) opens), on Linux:**
  `copy_file_range(2)`, with explicit offsets so that neither descriptor's
  position moves. The kernel shares the blocks where the filesystem can — a
  reflink on btrfs and XFS; on ZFS only with `zfs_bclone_enabled`, which
  OpenZFS 2.3 turns on and 2.2 leaves off — and copies in the kernel otherwise
  (ext4). Nothing passes through the process.
- **The kernel declines** (`ENOSYS`, `EXDEV`, `EINVAL`, `EIO`, `EOPNOTSUPP`,
  `EPERM`, the refusals Go's own `os.File.ReadFrom` falls back on): the copy
  goes on from where it stopped.
- **Otherwise:** `ReadAt` and `WriteAt`, 1 MiB at a time.

A kernel copy writes behind the driver's back, so `Range` then tells `dst`
its new end through `Truncate`, the only way the interface has; on the host
the file is already that long.

Pure Go, `CGO_ENABLED=0`. Tested on Linux (amd64, arm64, and under QEMU on
riscv64, loong64, ppc64le and s390x), macOS and Windows; built for the BSDs,
plan9, js, wasip1, 386 and arm.

## License

BSD-3-Clause, as [LICENSE](LICENSE) says.
