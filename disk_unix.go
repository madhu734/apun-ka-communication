//go:build linux

package main

import "syscall"

func diskAllowsUpload(dir string) bool {
	var fs syscall.Statfs_t
	if syscall.Statfs(dir, &fs) != nil || fs.Blocks == 0 {
		return false
	}
	return float64(fs.Bavail)/float64(fs.Blocks) >= 0.15
}
