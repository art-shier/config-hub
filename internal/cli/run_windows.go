//go:build windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func runPortableChild(
	ctx context.Context,
	argv, environment []string,
	stdout, stderr io.Writer,
) (int, error) {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Env = environment
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return 1, markRunExecution(fmt.Errorf("start child process: %w", err))
	}
	waitErr := command.Wait()
	if err := ctx.Err(); err != nil {
		return 1, markRunExecution(err)
	}
	if waitErr == nil {
		return 0, nil
	}
	var exitError *exec.ExitError
	if !errors.As(waitErr, &exitError) {
		return 1, markRunExecution(fmt.Errorf("wait for child process: %w", waitErr))
	}
	if code := exitError.ExitCode(); code >= 0 {
		return code, nil
	}
	return 1, markRunExecution(errors.New("child process exited without a status"))
}
