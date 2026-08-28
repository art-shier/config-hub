package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
)

func main() {
	if len(os.Args) == 1 {
		environment := os.Environ()
		sort.Strings(environment)
		for _, entry := range environment {
			fmt.Println(entry)
		}
		return
	}

	switch os.Args[1] {
	case "args":
		for _, argument := range os.Args[2:] {
			fmt.Println(argument)
		}
	case "touch":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Args[2], nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "exit":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		code, err := strconv.Atoi(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		os.Exit(code)
	case "signal-self":
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			os.Exit(1)
		}
		select {}
	case "wait-signal":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		code, err := strconv.Atoi(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(signals)
		group, err := syscall.Getpgid(0)
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("ready %d %d\n", os.Getpid(), group)
		fmt.Println(<-signals)
		os.Exit(code)
	case "wait-group":
		command := exec.Command(os.Args[0], "wait-signal", "23")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
}
