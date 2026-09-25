package p2pkey

// SetSyncDir replaces the directory sync used after publishing a new key file and returns a
// function that restores the original
func SetSyncDir(f func(dir string) error) (restore func()) {
	original := syncDir
	syncDir = f

	return func() {
		syncDir = original
	}
}

// StaleTempFileAge exposes the age after which leftover temporary key files are removed
const StaleTempFileAge = staleTempFileAge
