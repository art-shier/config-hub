package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

type configLoader func() (configSnapshot, error)

type runtimeConfigResolver func() (configSnapshot, error)

func newConfigCommand(load configLoader, resolve runtimeConfigResolver, stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect ConfigHub CLI configuration",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newConfigPathCommand(load, stdout))
	command.AddCommand(newConfigShowCommand(resolve, stdout))
	command.AddCommand(newConfigGetCommand(resolve, stdout))
	return command
}

func newConfigPathCommand(load configLoader, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print ConfigHub CLI configuration paths",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			snapshot, err := load()
			if err != nil {
				return err
			}
			output := "global: " + configStatusLabel(snapshot.Global) + "\n" +
				"local: " + configStatusLabel(snapshot.Local) + "\n"
			return writeCLICommandOutput(stdout, output)
		},
	}
}

func newConfigShowCommand(resolve runtimeConfigResolver, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print resolved ConfigHub CLI configuration",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			snapshot, err := resolve()
			if err != nil {
				return err
			}
			server, token := "<unset>", "<unset>"
			if snapshot.Server.Present {
				server = snapshot.Server.Value
			}
			if snapshot.Token.Present {
				token = maskConfigToken(snapshot.Token.Value)
			}
			output := "server: " + server + "\n" +
				"server_source: " + configSourceLabel(snapshot.Server) + "\n" +
				"token: " + token + "\n" +
				"token_source: " + configSourceLabel(snapshot.Token) + "\n"
			return writeCLICommandOutput(stdout, output)
		},
	}
}

func newConfigGetCommand(resolve runtimeConfigResolver, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "get server|token",
		Short: "Print one resolved ConfigHub CLI configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			snapshot, err := resolve()
			if err != nil {
				return err
			}
			var value configValue
			switch args[0] {
			case "server":
				value = snapshot.Server
			case "token":
				value = snapshot.Token
			default:
				return errLocalInput
			}
			if !value.Present {
				return errLocalInput
			}
			return writeCLICommandOutput(stdout, value.Value+"\n")
		},
	}
}

func configStatusLabel(status configFileStatus) string {
	path := status.Path
	if status.State == configUnavailable {
		path = "<unavailable>"
	}
	return fmt.Sprintf("%s (%s)", path, status.State)
}

func configSourceLabel(value configValue) string {
	if !value.Present {
		return string(sourceNone)
	}
	if (value.Source.Kind == sourceGlobal || value.Source.Kind == sourceLocal) && value.Source.Path != "" {
		return fmt.Sprintf("%s (%s)", value.Source.Kind, value.Source.Path)
	}
	return string(value.Source.Kind)
}

func maskConfigToken(token string) string {
	runes := []rune(token)
	prefixLength := 0
	if strings.HasPrefix(token, "ch_") {
		prefixLength = utf8.RuneCountInString("ch_")
	}
	const (
		suffixLength   = 4
		minimumMaskRun = 4
	)
	if len(runes) < prefixLength+suffixLength+minimumMaskRun {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:prefixLength]) +
		strings.Repeat("*", len(runes)-prefixLength-suffixLength) +
		string(runes[len(runes)-suffixLength:])
}

func writeCLICommandOutput(writer io.Writer, output string) error {
	written, err := io.WriteString(writer, output)
	if err != nil {
		return markRuntime(fmt.Errorf("%w: %w", errOutputWrite, err))
	}
	if written != len(output) {
		return markRuntime(fmt.Errorf("%w: %w", errOutputWrite, io.ErrShortWrite))
	}
	return nil
}
