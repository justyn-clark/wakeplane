package main

import (
	"fmt"
	"os"

	"github.com/justyn-clark/timekeeper/internal/cli"
)

const version = "0.1.0"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
