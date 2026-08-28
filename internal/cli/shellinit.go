package cli

import (
	"fmt"

	"gic/internal/session"
)

// ShellInit implements `gic shell-init`.
func ShellInit() {
	fmt.Print(session.ShellInit)
}
