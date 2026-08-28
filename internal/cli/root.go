package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

const maxTokenFileBytes = 4096

var (
	errCommandUsage = errors.New("command usage error")
	errLocalInput   = errors.New("local input error")
	errRuntime      = errors.New("runtime error")
)

func Execute(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	command := newRootCommand(ctx, getenv, stdout, stderr)
	command.SetArgs(args)
	err := command.Execute()
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errRuntime):
		fmt.Fprintln(stderr, "confighub: export failed")
		return 1
	default:
		fmt.Fprintln(stderr, "confighub: invalid command usage")
		return 2
	}
}

func newRootCommand(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) *cobra.Command {
	var serverURL, tokenFile string
	root := &cobra.Command{
		Use:           "confighub",
		Short:         "Read configuration from ConfigHub",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return errCommandUsage
		},
	}
	root.SetContext(ctx)
	root.SetOut(stderr)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&serverURL, "server", "", "ConfigHub server URL (or CONFIGHUB_URL)")
	root.PersistentFlags().StringVar(&tokenFile, "token-file", "", "read the machine token from a restricted file")

	var project, environment, service, format string
	export := &cobra.Command{
		Use:   "export",
		Short: "Export the current configuration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if (format != "json" && format != "dotenv") || !slugPattern.MatchString(project) || !slugPattern.MatchString(environment) {
				return errLocalInput
			}
			resolvedURL, err := resolveServerURL(root, serverURL, getenv)
			if err != nil {
				return errLocalInput
			}
			resolvedToken, err := resolveToken(root, tokenFile, getenv)
			if err != nil {
				return errLocalInput
			}
			client, err := NewClient(resolvedURL, resolvedToken)
			if err != nil {
				return errLocalInput
			}
			response, err := client.FetchConfig(command.Context(), project, environment, service)
			if err != nil {
				return errRuntime
			}
			if err := WriteExport(stdout, format, response); err != nil {
				return errRuntime
			}
			return nil
		},
	}
	export.Flags().StringVar(&project, "project", "", "project slug")
	export.Flags().StringVar(&environment, "env", "", "environment slug")
	export.Flags().StringVar(&service, "service", "", "optional service filter")
	export.Flags().StringVar(&format, "format", "", "output format: json or dotenv")
	_ = export.MarkFlagRequired("project")
	_ = export.MarkFlagRequired("env")
	_ = export.MarkFlagRequired("format")
	root.AddCommand(export)
	return root
}

func resolveServerURL(root *cobra.Command, flagValue string, getenv func(string) string) (string, error) {
	if root.PersistentFlags().Lookup("server").Changed {
		if flagValue == "" {
			return "", errLocalInput
		}
		return flagValue, nil
	}
	value := getenv("CONFIGHUB_URL")
	if value == "" {
		return "", errLocalInput
	}
	return value, nil
}

func resolveToken(root *cobra.Command, tokenFile string, getenv func(string) string) (string, error) {
	if root.PersistentFlags().Lookup("token-file").Changed {
		return readTokenFile(tokenFile)
	}
	token := getenv("CONFIGHUB_TOKEN")
	if !validToken(token) {
		return "", errLocalInput
	}
	return token, nil
}

func readTokenFile(path string) (string, error) {
	if path == "" {
		return "", errLocalInput
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", errLocalInput
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errLocalInput
	}
	content, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes+1))
	if err != nil || len(content) > maxTokenFileBytes {
		return "", errLocalInput
	}
	token := string(content)
	switch {
	case strings.HasSuffix(token, "\r\n"):
		token = strings.TrimSuffix(token, "\r\n")
	case strings.HasSuffix(token, "\n"):
		token = strings.TrimSuffix(token, "\n")
	}
	if !validToken(token) {
		return "", errLocalInput
	}
	return token, nil
}
