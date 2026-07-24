//go:build gogit

package gitdata

import "os"

// newBackend is the gogit-tagged backend selector: building with `-tags
// gogit` switches the default backend to the pure-Go go-git implementation.
// The GITLING_BACKEND environment variable can force shell-out even in a
// gogit-tagged binary (e.g. for A/B comparisons); it has no effect on
// binaries built without the tag, since openGogit doesn't exist there.
func newBackend(dir string) (Backend, error) {
	if os.Getenv("GITLING_BACKEND") == "shell" {
		return openShell(dir)
	}
	return openGogit(dir)
}
