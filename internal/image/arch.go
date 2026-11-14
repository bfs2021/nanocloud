package image

// NewManagerForArch creates an image Manager whose Catalog lists only images
// for arch ("amd64" or "arm64"). EntryByID still finds every entry.
func NewManagerForArch(cacheDir, arch string) *Manager {
	m := NewManager(cacheDir)
	m.arch = arch
	return m
}
