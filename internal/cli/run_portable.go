//go:build darwin || windows

package cli

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
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

	if err := ctx.Err(); err != nil {
		return 1, markRunExecution(err)
	}
	return runPortableChild(ctx, argv, childEnvironment, stdout, stderr)
}

type runExecutionFailure struct {
	cause error
}

func (e *runExecutionFailure) Error() string { return e.cause.Error() }
func (e *runExecutionFailure) Unwrap() error { return e.cause }

func markRunExecution(cause error) error {
	return &runExecutionFailure{cause: cause}
}
