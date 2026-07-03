//go:build !linux

package storagemon

func dropPageCache(fd int) {}