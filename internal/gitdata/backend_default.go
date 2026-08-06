package gitdata

// newBackend returns the shell-out backend, which is the only one.
func newBackend(dir string) (Backend, error) {
	return openShell(dir)
}
