package storage

// syncDir is a no-op on Windows: MOVEFILE_WRITE_THROUGH makes the rename
// durable and directories cannot be fsynced.
func syncDir(string) error { return nil }
