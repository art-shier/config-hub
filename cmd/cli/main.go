package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "confighub: not configured")
	os.Exit(2)
}
