//go:build linux

package storagemon

import "golang.org/x/sys/unix"

// dropPageCache evicts cached file pages so the next read hits storage.
func dropPageCache(fd int) {
	if fd < 0 {
		return
	}
	_ = unix.Fadvise(fd, 0, 0, unix.FADV_DONTNEED)
}
