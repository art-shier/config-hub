//go:build darwin || windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPortableRunnerRunsChildWithFetchedEnvironment(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{"CONFIGHUB_PORTABLE_VALUE": "from-config"}, nil
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := runner.Run(
		context.Background(),
		[]string{os.Args[0], "-test.run=^TestPortableRunHelper$"},
		[]string{
			"CONFIGHUB_PORTABLE_RUN_HELPER=1",
			"CONFIGHUB_PORTABLE_RUN_MODE=print",
			"CONFIGHUB_PORTABLE_VALUE=from-parent",
		},
		&stdout,
		&stderr,
	)
	if err != nil || code != 0 {
		t.Fatalf("Run() = (%d, %v), want (0, nil); stderr=%q", code, err, stderr.String())
	}
	if got := stdout.String(); got != "from-config\n" {
		t.Fatalf("stdout = %q, want fetched environment value", got)
	}
}

func TestPortableRunnerReturnsChildExitCode(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	code, err := runner.Run(
		context.Background(),
		[]string{os.Args[0], "-test.run=^TestPortableRunHelper$"},
		[]string{
			"CONFIGHUB_PORTABLE_RUN_HELPER=1",
			"CONFIGHUB_PORTABLE_RUN_MODE=exit",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err != nil || code != 7 {
		t.Fatalf("Run() = (%d, %v), want (7, nil)", code, err)
	}
}

func TestPortableRunnerCancelsChild(t *testing.T) {
	runner := Runner{Fetch: func(context.Context) (map[string]string, error) {
		return map[string]string{}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	code, err := runner.Run(
		ctx,
		[]string{os.Args[0], "-test.run=^TestPortableRunHelper$"},
		[]string{
			"CONFIGHUB_PORTABLE_RUN_HELPER=1",
			"CONFIGHUB_PORTABLE_RUN_MODE=wait",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if code != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() = (%d, %v), want (1, context deadline exceeded)", code, err)
	}
}

func TestPortableRunHelper(t *testing.T) {
	if os.Getenv("CONFIGHUB_PORTABLE_RUN_HELPER") != "1" {
		return
	}
	switch os.Getenv("CONFIGHUB_PORTABLE_RUN_MODE") {
	case "print":
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("CONFIGHUB_PORTABLE_VALUE"))
		os.Exit(0)
	case "exit":
		os.Exit(7)
	case "wait":
		time.Sleep(30 * time.Second)
	default:
		os.Exit(2)
	}
}
