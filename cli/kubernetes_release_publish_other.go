//go:build !linux

package main

import "errors"

func publishKubernetesReleaseDirectory(_, _ string) error {
	return errors.New("atomic kubernetes release publication requires Linux renameat2")
}
