package main

import (
	"context"
	"os"

	"github.com/utsavgupta5467/skein/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
