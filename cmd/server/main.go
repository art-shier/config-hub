package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "confighub-server: not configured")
	os.Exit(2)
}
