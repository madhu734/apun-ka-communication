package main

import "golang.org/x/sys/windows"

func diskAllowsUpload(dir string) bool {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return false
	}
	var available, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(path, &available, &total, &totalFree) != nil || total == 0 {
		return false
	}
	return float64(available)/float64(total) >= 0.15
}
