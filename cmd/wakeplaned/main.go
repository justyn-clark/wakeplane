// wakeplaned is a conventional alias for the wakeplane binary.
// Both binaries expose the same command surface. The "d" suffix
// follows the Unix convention of naming daemon binaries separately
// (e.g., sshd, httpd) so that process listings and packaging tools
// can distinguish the long-running daemon from CLI invocations.
// If a future release splits the CLI from the daemon, this entry
// point will diverge to expose only "serve" and related commands.
package main

import (
	"fmt"
	"os"

	"github.com/justyn-clark/wakeplane/internal/cli"
)

const version = "0.2.0-beta.1"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
