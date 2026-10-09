// Copyright (c) 2026, go-filesystems
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package hostcopy

import filesystem "github.com/go-filesystems/interface"

// kernelCopy copies nothing outside Linux: the caller goes on with ReadAt
// and WriteAt.
func kernelCopy(_, _ filesystem.HostFile, _, _, _ int64) (int64, bool, error) { return 0, false, nil }
