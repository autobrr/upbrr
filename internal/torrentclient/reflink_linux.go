// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux

package torrentclient

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	reflinkCloneRetryAttempts  = 5
	reflinkCloneRetryBaseDelay = 25 * time.Millisecond
)

var (
	ioctlFileClone      = unix.IoctlFileClone
	ioctlFileCloneRange = unix.IoctlFileCloneRange
	sleepForRetry       = time.Sleep
)

func reflinkFile(src, dst string) (retErr error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if srcInfo.IsDir() {
		return errors.New("source is a directory")
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		_ = dstFile.Close()
		if retErr != nil {
			_ = os.Remove(dst)
		}
	}()

	srcFd := int(srcFile.Fd())
	dstFd := int(dstFile.Fd())

	var cloneErr error
	for attempt := range reflinkCloneRetryAttempts {
		cloneErr = ioctlFileClone(dstFd, srcFd)
		if cloneErr == nil {
			return nil
		}
		if shouldRetryCloneError(cloneErr) {
			if attempt == reflinkCloneRetryAttempts-1 {
				return fmt.Errorf("ioctl FICLONE: %w (retries exhausted)", cloneErr)
			}
			sleepForRetry(reflinkCloneRetryBaseDelay * time.Duration(1<<attempt))
			continue
		}
		break
	}

	if shouldTryCloneRange(cloneErr) {
		cloneRange := unix.FileCloneRange{
			Src_fd:      int64(srcFd),
			Src_offset:  0,
			Src_length:  0,
			Dest_offset: 0,
		}
		if rangeErr := ioctlFileCloneRange(dstFd, &cloneRange); rangeErr != nil {
			return fmt.Errorf("ioctl FICLONERANGE: %w", rangeErr)
		}
		return nil
	}

	return fmt.Errorf("ioctl FICLONE: %w", cloneErr)
}

func shouldRetryCloneError(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINVAL)
}

func shouldTryCloneRange(err error) bool {
	return errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTTY) ||
		errors.Is(err, unix.ENOSYS)
}
