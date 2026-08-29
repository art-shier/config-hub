package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type Runner struct {
	Fetch func(context.Context) (map[string]string, error)
}

func (r Runner) Run(
	ctx context.Context,
	argv, parentEnv []string,
	stdout, stderr io.Writer,
) (exitCode int, runErr error) {
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

	if err := ctx.Err(); err != nil {
		return 1, markRunExecution(err)
	}
	command := exec.Command(argv[0], argv[1:]...)
	command.Env = childEnvironment
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	terminal := ownedForegroundTerminal(os.Stdin)
	terminalRestored := terminal == nil
	restoreTerminal := func() error {
		if terminalRestored {
			return nil
		}
		terminalRestored = true
		return terminal.restore()
	}
	defer func() {
		if err := restoreTerminal(); err != nil {
			restoreFailure := markRunExecution(fmt.Errorf("restore terminal foreground: %w", err))
			exitCode = 1
			if runErr == nil {
				runErr = restoreFailure
			} else {
				runErr = errors.Join(runErr, restoreFailure)
			}
		}
	}()
	if terminal == nil {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else {
		command.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: terminal.fd}
	}

	signals := make(chan os.Signal, 2)
	childSignals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(childSignals, syscall.SIGCHLD)
	signalsStopped := false
	stopSignalDelivery := func() {
		if !signalsStopped {
			signal.Stop(signals)
			signal.Stop(childSignals)
			signalsStopped = true
		}
	}
	defer stopSignalDelivery()
	if err := command.Start(); err != nil {
		return 1, markRunExecution(fmt.Errorf("start child process: %w", err))
	}

	terminateChildGroup := func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		leaderErr := command.Process.Kill()
		if leaderErr != nil && !errors.Is(leaderErr, os.ErrProcessDone) && !errors.Is(leaderErr, syscall.ESRCH) {
			return errors.Join(err, leaderErr)
		}
		return err
	}
	finishChild := func(observationErr, chosenErr error) (int, error) {
		stopSignalDelivery()
		restoreErr := restoreTerminal()
		waitErr := command.Wait()

		var failures []error
		if chosenErr != nil {
			failures = append(failures, chosenErr)
		}
		if observationErr != nil {
			failures = append(failures, fmt.Errorf("observe child process: %w", observationErr))
		}
		if restoreErr != nil {
			failures = append(failures, fmt.Errorf("restore terminal foreground: %w", restoreErr))
		}
		if len(failures) > 0 {
			var exitError *exec.ExitError
			if waitErr != nil && !errors.As(waitErr, &exitError) {
				failures = append(failures, fmt.Errorf("wait for child process: %w", waitErr))
			}
			return 1, markRunExecution(errors.Join(failures...))
		}

		code, resultErr := childResult(waitErr)
		if resultErr != nil {
			return code, markRunExecution(resultErr)
		}
		return code, nil
	}
	terminateAndFinish := func(chosenErr error) (int, error) {
		if err := terminateChildGroup(); err != nil {
			chosenErr = errors.Join(chosenErr, fmt.Errorf("terminate child process group: %w", err))
		}
		observed, observationErr := observeChildLeaderExit(command.Process.Pid, false)
		if observationErr == nil && !observed {
			observationErr = errors.New("waitid returned without observing child exit")
		}
		return finishChild(observationErr, chosenErr)
	}
	finishIfLeaderExited := func() (bool, int, error) {
		exited, err := observeChildLeaderExit(command.Process.Pid, true)
		if err != nil {
			code, resultErr := terminateAndFinish(fmt.Errorf("probe child process: %w", err))
			return true, code, resultErr
		}
		if !exited {
			return false, 0, nil
		}
		code, resultErr := finishChild(nil, nil)
		return true, code, resultErr
	}

	for {
		select {
		case <-childSignals:
			exited, err := observeChildLeaderExit(command.Process.Pid, true)
			if err != nil {
				return terminateAndFinish(fmt.Errorf("observe child process: %w", err))
			}
			if exited {
				return finishChild(nil, nil)
			}
		case <-ctx.Done():
			if finished, code, err := finishIfLeaderExited(); finished {
				return code, err
			}
			return terminateAndFinish(ctx.Err())
		case received := <-signals:
			forwarded, ok := received.(syscall.Signal)
			if !ok {
				continue
			}
			if finished, code, err := finishIfLeaderExited(); finished {
				return code, err
			}
			if err := syscall.Kill(-command.Process.Pid, forwarded); err != nil && !errors.Is(err, syscall.ESRCH) {
				return terminateAndFinish(fmt.Errorf("signal child process group: %w", err))
			}
		}
	}
}

func observeChildLeaderExit(pid int, nonblocking bool) (bool, error) {
	options := unix.WEXITED | unix.WNOWAIT
	if nonblocking {
		options |= unix.WNOHANG
	}
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, options, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		return info.Code != 0, nil
	}
}

type foregroundTerminal struct {
	fd            int
	originalGroup int
}

func ownedForegroundTerminal(file *os.File) *foregroundTerminal {
	fd := int(file.Fd())
	foregroundGroup, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil || foregroundGroup != unix.Getpgrp() {
		return nil
	}
	return &foregroundTerminal{fd: fd, originalGroup: foregroundGroup}
}

func (terminal *foregroundTerminal) restore() error {
	signalNumber := int(unix.SIGTTOU) - 1
	blocked := unix.Sigset_t{}
	blocked.Val[signalNumber/64] |= uint64(1) << uint(signalNumber%64)
	var originalMask unix.Sigset_t

	runtime.LockOSThread()
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &blocked, &originalMask); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("block SIGTTOU: %w", err)
	}
	restoreForegroundErr := unix.IoctlSetPointerInt(terminal.fd, unix.TIOCSPGRP, terminal.originalGroup)
	restoreMaskErr := unix.PthreadSigmask(unix.SIG_SETMASK, &originalMask, nil)
	runtime.UnlockOSThread()

	if restoreForegroundErr != nil {
		return restoreForegroundErr
	}
	if restoreMaskErr != nil {
		return fmt.Errorf("restore signal mask: %w", restoreMaskErr)
	}
	return nil
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
