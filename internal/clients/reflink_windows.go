// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package clients

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlDuplicateExtentsToFile = 0x00098344
	fsctlSetSparse              = 0x000900C4
	maxCloneChunkSize           = 1024 * 1024 * 1024
	copyBufferSize              = 1024 * 1024
	refsFilesystemName          = "REFS"
	fileAttributeSparseFile     = uintptr(0x00000200)
	invalidFileAttributes       = uintptr(0xFFFFFFFF)
	fileBegin                   = 0
)

type duplicateExtentsData struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

var (
	kernel32DLL             = windows.NewLazySystemDLL("kernel32.dll")
	procGetDiskFreeSpaceW   = kernel32DLL.NewProc("GetDiskFreeSpaceW")
	procGetFileAttributesW  = kernel32DLL.NewProc("GetFileAttributesW")
	procSetFilePointerEx    = kernel32DLL.NewProc("SetFilePointerEx")
	procSetEndOfFile        = kernel32DLL.NewProc("SetEndOfFile")
	reflinkEvalSymlinks     = filepath.EvalSymlinks
	reflinkCopyBufferPool   = sync.Pool{New: func() any { buffer := make([]byte, copyBufferSize); return &buffer }}
	errReflinkNotSupported  = errors.New("reflink is not supported")
	errReflinkVolumeInvalid = errors.New("source and destination must be on the same ReFS volume")
)

func reflinkFile(src, dst string) (retErr error) {
	resolvedSrc, err := reflinkEvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	srcFile, err := os.Open(resolvedSrc)
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

	dstParent := filepath.Dir(dst)
	if dstParent == "" {
		dstParent = "."
	}
	resolvedDstParent, err := reflinkEvalSymlinks(dstParent)
	if err != nil {
		return fmt.Errorf("resolve destination parent: %w", err)
	}

	volumeRoot, err := ensureSameReflinkVolume(resolvedSrc, resolvedDstParent)
	if err != nil {
		return err
	}
	clusterSize, err := ensureReFSVolume(volumeRoot)
	if err != nil {
		return err
	}

	sourceIsSparse, err := isSparseFile(resolvedSrc)
	if err != nil {
		return fmt.Errorf("check source sparse flag: %w", err)
	}

	dstFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		_ = dstFile.Close()
		if retErr != nil {
			_ = os.Remove(dst)
		}
	}()

	srcHandle := windows.Handle(srcFile.Fd())
	dstHandle := windows.Handle(dstFile.Fd())
	if sourceIsSparse {
		if err := markFileSparse(dstHandle, dst); err != nil {
			return fmt.Errorf("mark destination sparse: %w", err)
		}
	}
	if err := setFileEnd(dstHandle, dst, srcInfo.Size()); err != nil {
		return fmt.Errorf("resize destination: %w", err)
	}

	cloneableSize := srcInfo.Size() - (srcInfo.Size() % clusterSize)
	for offset := int64(0); offset < cloneableSize; offset += maxCloneChunkSize {
		chunkSize := min(maxCloneChunkSize, cloneableSize-offset)
		if err := duplicateExtent(dstHandle, srcHandle, offset, offset, chunkSize); err != nil {
			if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
				return fmt.Errorf("%w: duplicate extents unsupported for this file or volume", errReflinkNotSupported)
			}
			return fmt.Errorf("duplicate extents: %w", err)
		}
	}

	if tailSize := srcInfo.Size() - cloneableSize; tailSize > 0 {
		if err := copyFileTail(srcFile, dstFile, cloneableSize, tailSize); err != nil {
			return fmt.Errorf("copy file tail: %w", err)
		}
	}

	return nil
}

func ensureSameReflinkVolume(src, dst string) (string, error) {
	srcRoot, err := volumeRootForPath(src)
	if err != nil {
		return "", fmt.Errorf("get source volume: %w", err)
	}
	dstRoot, err := volumeRootForPath(dst)
	if err != nil {
		return "", fmt.Errorf("get destination volume: %w", err)
	}
	if !strings.EqualFold(srcRoot, dstRoot) {
		return "", errReflinkVolumeInvalid
	}
	return srcRoot, nil
}

func ensureReFSVolume(volumeRoot string) (int64, error) {
	filesystemName, err := filesystemNameForVolume(volumeRoot)
	if err != nil {
		return 0, err
	}
	if !strings.EqualFold(filesystemName, refsFilesystemName) {
		return 0, fmt.Errorf("%w: volume %s is %s, not ReFS", errReflinkVolumeInvalid, volumeRoot, filesystemName)
	}
	clusterSize, err := clusterSizeForVolume(volumeRoot)
	if err != nil {
		return 0, err
	}
	if clusterSize <= 0 {
		return 0, errors.New("invalid cluster size")
	}
	return clusterSize, nil
}

func volumeRootForPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("abs path: %w", err)
	}
	volumePath := make([]uint16, windows.MAX_PATH+1)
	pathPtr, err := windows.UTF16PtrFromString(absPath)
	if err != nil {
		return "", fmt.Errorf("convert path: %w", err)
	}
	volumePathLen, err := uint32Len("volume path buffer", len(volumePath))
	if err != nil {
		return "", err
	}
	if err := windows.GetVolumePathName(pathPtr, &volumePath[0], volumePathLen); err != nil {
		return "", fmt.Errorf("get volume path name: %w", err)
	}
	volumeRoot := windows.UTF16ToString(volumePath)
	if !strings.HasSuffix(volumeRoot, `\`) {
		volumeRoot += `\`
	}
	return volumeRoot, nil
}

func filesystemNameForVolume(volumeRoot string) (string, error) {
	volumePathPtr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return "", fmt.Errorf("convert volume path: %w", err)
	}
	filesystemName := make([]uint16, windows.MAX_PATH+1)
	var volumeSerial uint32
	var maxComponentLength uint32
	var flags uint32
	filesystemNameLen, err := uint32Len("filesystem name buffer", len(filesystemName))
	if err != nil {
		return "", err
	}
	if err := windows.GetVolumeInformation(
		volumePathPtr,
		nil,
		0,
		&volumeSerial,
		&maxComponentLength,
		&flags,
		&filesystemName[0],
		filesystemNameLen,
	); err != nil {
		return "", fmt.Errorf("get volume information: %w", err)
	}
	name := windows.UTF16ToString(filesystemName)
	if name == "" {
		return "", errors.New("filesystem name is empty")
	}
	return name, nil
}

func clusterSizeForVolume(volumeRoot string) (int64, error) {
	volumePathPtr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return 0, fmt.Errorf("convert volume path: %w", err)
	}
	var sectorsPerCluster uint32
	var bytesPerSector uint32
	var freeClusters uint32
	var totalClusters uint32
	r1, _, callErr := procGetDiskFreeSpaceW.Call(
		uintptr(unsafe.Pointer(volumePathPtr)),
		uintptr(unsafe.Pointer(&sectorsPerCluster)),
		uintptr(unsafe.Pointer(&bytesPerSector)),
		uintptr(unsafe.Pointer(&freeClusters)),
		uintptr(unsafe.Pointer(&totalClusters)),
	)
	if r1 == 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return 0, fmt.Errorf("get cluster size: %w", callErr)
		}
		return 0, errors.New("get cluster size: unknown error")
	}
	return int64(sectorsPerCluster) * int64(bytesPerSector), nil
}

func isSparseFile(path string) (bool, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, fmt.Errorf("convert path: %w", err)
	}
	r1, _, callErr := procGetFileAttributesW.Call(uintptr(unsafe.Pointer(pathPtr)))
	if r1 == invalidFileAttributes {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return false, fmt.Errorf("get file attributes: %w", callErr)
		}
		return false, errors.New("get file attributes: unknown error")
	}
	return r1&fileAttributeSparseFile != 0, nil
}

func markFileSparse(fileHandle windows.Handle, path string) error {
	var bytesReturned uint32
	if err := windows.DeviceIoControl(fileHandle, fsctlSetSparse, nil, 0, nil, 0, &bytesReturned, nil); err != nil {
		return fmt.Errorf("set sparse %s: %w", path, err)
	}
	return nil
}

func setFileEnd(fileHandle windows.Handle, path string, size int64) error {
	var newPosition int64
	sizeArg, err := uintptrFromNonNegativeInt64("file size", size)
	if err != nil {
		return err
	}
	r1, _, callErr := procSetFilePointerEx.Call(
		uintptr(fileHandle),
		sizeArg,
		uintptr(unsafe.Pointer(&newPosition)),
		uintptr(fileBegin),
	)
	if r1 == 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("seek EOF for %s: %w", path, callErr)
		}
		return fmt.Errorf("seek EOF for %s: unknown error", path)
	}
	r1, _, callErr = procSetEndOfFile.Call(uintptr(fileHandle))
	if r1 == 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("set EOF for %s: %w", path, callErr)
		}
		return fmt.Errorf("set EOF for %s: unknown error", path)
	}
	return nil
}

func duplicateExtent(targetHandle, sourceHandle windows.Handle, sourceOffset, targetOffset, byteCount int64) error {
	data := duplicateExtentsData{
		FileHandle:       sourceHandle,
		SourceFileOffset: sourceOffset,
		TargetFileOffset: targetOffset,
		ByteCount:        byteCount,
	}
	var bytesReturned uint32
	if err := windows.DeviceIoControl(
		targetHandle,
		fsctlDuplicateExtentsToFile,
		(*byte)(unsafe.Pointer(&data)),
		uint32(unsafe.Sizeof(data)),
		nil,
		0,
		&bytesReturned,
		nil,
	); err != nil {
		return fmt.Errorf("duplicate extents: %w", err)
	}
	return nil
}

func copyFileTail(srcFile, dstFile *os.File, offset, length int64) error {
	if _, err := srcFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek source: %w", err)
	}
	if _, err := dstFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek destination: %w", err)
	}
	buffer, ok := reflinkCopyBufferPool.Get().(*[]byte)
	if !ok || buffer == nil {
		buffer = newReflinkCopyBuffer()
	}
	defer reflinkCopyBufferPool.Put(buffer)
	copied, err := io.CopyBuffer(dstFile, io.LimitReader(srcFile, length), *buffer)
	if err != nil {
		return fmt.Errorf("copy tail: %w", err)
	}
	if copied != length {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func newReflinkCopyBuffer() *[]byte {
	buffer := make([]byte, copyBufferSize)
	return &buffer
}

func uint32Len(label string, value int) (uint32, error) {
	if value < 0 || uint64(value) > math.MaxUint32 {
		return 0, fmt.Errorf("%s length overflows uint32", label)
	}
	return uint32(value), nil
}

func uintptrFromNonNegativeInt64(label string, value int64) (uintptr, error) {
	if value < 0 || uint64(value) > uint64(^uintptr(0)) {
		return 0, fmt.Errorf("%s overflows uintptr", label)
	}
	return uintptr(value), nil
}
