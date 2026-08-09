//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package opencode

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {
	// CommandContext supplies the platform fallback. The generated Birdy tool
	// independently enforces its own deadline and kills its child.
}
