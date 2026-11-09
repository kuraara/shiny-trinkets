//go:build !linux

package main

import "io/fs"

// Outside Linux (the Windows scanner, later) there are no device numbers or block counts in
// FileInfo yet: sizes are apparent sizes and mount points aren't detected.
func statOf(fi fs.FileInfo) fstat {
	return fstat{size: fi.Size(), blocks: fi.Size(), nlink: 1}
}

func fsType(string) string { return "unknown" }
