package main

import (
	"fmt"
	"os"

	"github.com/justyn-clark/wakeplane/internal/cli"
)

const version = "0.3.0-beta.3"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
