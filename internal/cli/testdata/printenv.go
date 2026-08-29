package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"
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
	case "pid-exit":
		if len(os.Args) != 3 {
			os.Exit(2)
		}
		code, err := strconv.Atoi(os.Args[2])
		if err != nil {
			os.Exit(2)
		}
		fmt.Printf("exit-pid %d\n", os.Getpid())
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
		signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
		command := exec.Command(os.Args[0], "wait-signal", "23")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			os.Exit(1)
		}
	case "read-line":
		group, err := syscall.Getpgid(0)
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("tty-ready %d %d\n", os.Getpid(), group)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			os.Exit(1)
		}
		fmt.Printf("tty-read %s\n", scanner.Text())
	case "exit-with-grandchild":
		if len(os.Args) != 5 {
			os.Exit(2)
		}
		command := exec.Command(os.Args[0], "signal-marker-grandchild", os.Args[2], os.Args[3], os.Args[4])
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			os.Exit(1)
		}
		group, err := syscall.Getpgid(0)
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("race-leader %d %d %d\n", os.Getpid(), group, command.Process.Pid)
		if !waitForFile(os.Args[2], 5*time.Second) {
			_ = command.Process.Kill()
			os.Exit(1)
		}
		os.Exit(42)
	case "signal-marker-grandchild":
		if len(os.Args) != 5 {
			os.Exit(2)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
		if err := os.WriteFile(os.Args[2], nil, 0o600); err != nil {
			os.Exit(1)
		}
		for {
			select {
			case <-signals:
				if err := os.WriteFile(os.Args[3], nil, 0o600); err != nil {
					os.Exit(1)
				}
			default:
				if _, err := os.Stat(os.Args[4]); err == nil {
					return
				} else if !os.IsNotExist(err) {
					os.Exit(1)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	default:
		os.Exit(2)
	}
}

func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		} else if !os.IsNotExist(err) {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}
