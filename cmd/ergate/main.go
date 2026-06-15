package main

import (
	"fmt"
	"os"

	"github.com/raydraw/ergate/internal/cli"
)

func main() {
	cmd := cli.RootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
