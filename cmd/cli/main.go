package main

import (
	"context"
	"os"

	"confighub.local/internal/cli"
)

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
