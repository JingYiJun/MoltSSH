package main

import (
	"fmt"
	"os"

	"github.com/jingyijun/moltssh/internal/buildinfo"
	"github.com/jingyijun/moltssh/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, buildinfo.Current()); err != nil {
		fmt.Fprintf(os.Stderr, "moltssh: %v\n", err)
		os.Exit(1)
	}
}
