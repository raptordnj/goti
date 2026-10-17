//go:build !linux

package storage

import "os"

func preallocate(*os.File, int64) {}
