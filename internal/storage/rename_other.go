//go:build unix && !linux && !darwin

package storage

// renameNoReplace moves old to new without replacing new, using link and
// unlink since these platforms lack a portable atomic no-replace rename.
func renameNoReplace(old, newPath string) error {
	return linkRename(old, newPath)
}
