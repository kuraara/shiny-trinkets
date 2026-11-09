//go:build linux

package main

import (
	"fmt"
	"io/fs"
	"syscall"
)

func statOf(fi fs.FileInfo) fstat {
	st := fstat{size: fi.Size(), blocks: fi.Size(), nlink: 1}
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		st.dev = uint64(s.Dev)
		st.ino = uint64(s.Ino)
		st.nlink = uint64(s.Nlink)
		st.blocks = int64(s.Blocks) * 512
	}
	return st
}

func fsType(path string) string {
	var sf syscall.Statfs_t
	if err := syscall.Statfs(path, &sf); err != nil {
		return "unknown"
	}
	t := uint64(uint32(sf.Type)) // magic numbers are 32-bit; Type is signed on some arches
	if n, ok := fsNames[t]; ok {
		return n
	}
	return fmt.Sprintf("0x%x", t)
}
