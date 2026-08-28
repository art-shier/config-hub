package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
)

type Runner struct {
	Fetch func(context.Context) (map[string]string, error)
}

func (r Runner) Run(
	ctx context.Context,
	argv, parentEnv []string,
	stdout, stderr io.Writer,
) (int, error) {
	if len(argv) == 0 || argv[0] == "" {
		return 1, markRunExecution(errors.New("child command is required"))
	}
	if r.Fetch == nil {
		return 1, markRunExecution(errors.New("configuration fetch is unavailable"))
	}
	values, err := r.Fetch(ctx)
	if err != nil {
		return 1, err
	}
	for key, value := range values {
		if !environmentKeyPattern.MatchString(key) || !validEnvironmentValue(value) {
			return 1, markRunExecution(errors.New("invalid child environment"))
		}
	}

	environment := make(map[string]string, len(parentEnv)+len(values))
	for _, entry := range parentEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsRune(key, '\x00') || !validEnvironmentValue(value) {
			return 1, markRunExecution(errors.New("invalid parent environment"))
		}
		environment[key] = value
	}
	for key, value := range values {
		environment[key] = value
	}
	childEnvironment := make([]string, 0, len(environment))
	for key, value := range environment {
		childEnvironment = append(childEnvironment, key+"="+value)
	}
	sort.Strings(childEnvironment)

	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Env = childEnvironment
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := command.Start(); err != nil {
		return 1, markRunExecution(fmt.Errorf("start child process: %w", err))
	}

	wait := make(chan error, 1)
	go func() {
		wait <- command.Wait()
	}()
	for {
		select {
		case err := <-wait:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 1, markRunExecution(ctxErr)
			}
			code, resultErr := childResult(err)
			if resultErr != nil {
				return code, markRunExecution(resultErr)
			}
			return code, nil
		case received := <-signals:
			forwarded, ok := received.(syscall.Signal)
			if !ok {
				continue
			}
			if err := syscall.Kill(-command.Process.Pid, forwarded); err != nil && !errors.Is(err, syscall.ESRCH) {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				_ = command.Process.Kill()
				<-wait
				return 1, markRunExecution(fmt.Errorf("signal child process group: %w", err))
			}
		}
	}
}

type runExecutionFailure struct {
	cause error
}

func (e *runExecutionFailure) Error() string { return e.cause.Error() }
func (e *runExecutionFailure) Unwrap() error { return e.cause }

func markRunExecution(cause error) error {
	return &runExecutionFailure{cause: cause}
}

func childResult(err error) (int, error) {
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
	return 1, fmt.Errorf("child process exited without a status")
}
