//go:build darwin

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func runPortableChild(
	ctx context.Context,
	argv, environment []string,
	stdout, stderr io.Writer,
) (int, error) {
	command := exec.Command(argv[0], argv[1:]...)
	command.Env = environment
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return 1, markRunExecution(fmt.Errorf("start child process: %w", err))
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- command.Wait()
	}()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	for {
		select {
		case waitErr := <-waitDone:
			code, resultErr := portableDarwinChildResult(waitErr)
			if resultErr != nil {
				return code, markRunExecution(resultErr)
			}
			return code, nil
		case <-ctx.Done():
			killErr := command.Process.Kill()
			waitErr := <-waitDone
			var failures []error
			failures = append(failures, ctx.Err())
			if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				failures = append(failures, fmt.Errorf("terminate child process: %w", killErr))
			}
			var exitError *exec.ExitError
			if waitErr != nil && !errors.As(waitErr, &exitError) {
				failures = append(failures, fmt.Errorf("wait for child process: %w", waitErr))
			}
			return 1, markRunExecution(errors.Join(failures...))
		case received := <-signals:
			if err := command.Process.Signal(received); err != nil && !errors.Is(err, os.ErrProcessDone) {
				killErr := command.Process.Kill()
				waitErr := <-waitDone
				failures := []error{fmt.Errorf("signal child process: %w", err)}
				if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
					failures = append(failures, fmt.Errorf("terminate child process: %w", killErr))
				}
				var exitError *exec.ExitError
				if waitErr != nil && !errors.As(waitErr, &exitError) {
					failures = append(failures, fmt.Errorf("wait for child process: %w", waitErr))
				}
				return 1, markRunExecution(errors.Join(failures...))
			}
		}
	}
}

func portableDarwinChildResult(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return 1, fmt.Errorf("wait for child process: %w", err)
	}
	if code := exitError.ExitCode(); code >= 0 {
		return code, nil
	}
	if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return 1, errors.New("child process exited without a status")
}
