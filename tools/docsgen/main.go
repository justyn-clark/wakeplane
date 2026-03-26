package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/justyn-clark/wakeplane/internal/docsgen"
)

func main() {
	check := flag.Bool("check", false, "verify generated docs are up to date")
	flag.Parse()

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *check {
		if err := docsgen.Check(repoRoot); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := docsgen.Write(repoRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
