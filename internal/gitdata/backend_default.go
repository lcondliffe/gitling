//go:build !gogit

package gitdata

// newBackend is the default (non-gogit-tagged) backend selector: it always
// returns the shell-out backend, so the default build stays zero-external-
// dependency. Build with `-tags gogit` to opt into the pure-Go backend
// instead (see backend_gogit.go).
func newBackend(dir string) (Backend, error) {
	return openShell(dir)
}
