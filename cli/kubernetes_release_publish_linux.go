//go:build linux

package main

import "golang.org/x/sys/unix"

func publishKubernetesReleaseDirectory(stagingRoot, outputRoot string) error {
	return unix.Renameat2(
		unix.AT_FDCWD,
		stagingRoot,
		unix.AT_FDCWD,
		outputRoot,
		unix.RENAME_NOREPLACE,
	)
}
