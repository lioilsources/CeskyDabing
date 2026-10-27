package main

import "golang.org/x/sys/unix"

func fsType(path string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return ""
	}
	return unix.ByteSliceToString(st.Fstypename[:])
}
