// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

// Package hostcopy copies a range of one driver file into another without
// holding the whole file in memory, and without passing it through this
// process at all when both are files of the host.
//
// It is what a file server's server-side copy is made of -- WebDAV COPY, SFTP
// copy-data, SMB FSCTL_SRV_COPYCHUNK, S3 CopyObject -- where the alternative,
// ReadFile then WriteFile, allocates the size of the file being copied.
//
// When both files are [filesystem.HostFile] and the system is Linux, the copy
// is copy_file_range(2), with explicit offsets so that neither descriptor's
// position moves. The kernel then shares the blocks where the filesystem can
// (a reflink on btrfs and XFS; on ZFS only with zfs_bclone_enabled, which
// OpenZFS 2.3 turns on and 2.2 leaves off) and copies them in the kernel
// otherwise (ext4). Where the kernel declines -- across filesystems, or on a
// system without the call -- the copy goes on with ReadAt and WriteAt, one
// MiB at a time, from where the kernel stopped.
package hostcopy
