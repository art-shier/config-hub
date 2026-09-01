package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const (
	maxMutationValueBytes   = 1 << 20
	maxMutationMessageBytes = 1024
)

func newMutationCommands(resolveConfig func() (configSnapshot, error), stdoutWriter interface{ Write([]byte) (int, error) }) []*cobra.Command {
	var setProject, setEnvironment, setService, setMessage string
	set := &cobra.Command{
		Use:   "set --project P --env E [--service S] [--message M] KEY=VALUE",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, argv []string) error {
			key, value, err := parseSetArgument(argv[0])
			if err != nil || len(key) > mutationClientMaxKeyBytes || !slugPattern.MatchString(setProject) || !slugPattern.MatchString(setEnvironment) || !validMutationService(setService) || !validMutationMessage(setMessage) {
				return errLocalInput
			}
			if !command.Flags().Changed("message") {
				setMessage = fmt.Sprintf("Set %s via CLI", key)
			}
			snapshot, err := resolveConfig()
			if err != nil {
				return err
			}
			serverURL, token, err := requireConnectionConfig(snapshot)
			if err != nil {
				return errLocalInput
			}
			client, err := NewClient(serverURL, token)
			if err != nil {
				return errLocalInput
			}
			current, err := client.FetchConfig(command.Context(), setProject, setEnvironment, "")
			if err != nil {
				return markMutationRuntime("set", err)
			}
			var servicePointer *string
			if command.Flags().Changed("service") {
				servicePointer = &setService
			}
			result, err := client.MutateConfig(command.Context(), setProject, setEnvironment, MutationRequest{
				BaseRevision: current.Revision,
				Message:      setMessage,
				Operation: MutationOperation{
					Type:    "set",
					Key:     key,
					Value:   &value,
					Service: servicePointer,
				},
			})
			if err != nil {
				return markMutationRuntime("set", err)
			}
			return writeCLICommandOutput(stdoutWriter, fmt.Sprintf("revision %d\n", result.Revision))
		},
	}
	set.Flags().StringVar(&setProject, "project", "", "project slug")
	set.Flags().StringVar(&setEnvironment, "env", "", "environment slug")
	set.Flags().StringVar(&setService, "service", "", "optional service filter")
	set.Flags().StringVar(&setMessage, "message", "", "mutation message")
	_ = set.MarkFlagRequired("project")
	_ = set.MarkFlagRequired("env")

	var unsetProject, unsetEnvironment, unsetMessage string
	unset := &cobra.Command{
		Use:   "unset --project P --env E [--message M] KEY",
		Short: "Unset a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, argv []string) error {
			key := argv[0]
			if !environmentKeyPattern.MatchString(key) || len(key) > mutationClientMaxKeyBytes || !slugPattern.MatchString(unsetProject) || !slugPattern.MatchString(unsetEnvironment) || !validMutationMessage(unsetMessage) {
				return errLocalInput
			}
			if !command.Flags().Changed("message") {
				unsetMessage = fmt.Sprintf("Unset %s via CLI", key)
			}
			snapshot, err := resolveConfig()
			if err != nil {
				return err
			}
			serverURL, token, err := requireConnectionConfig(snapshot)
			if err != nil {
				return errLocalInput
			}
			client, err := NewClient(serverURL, token)
			if err != nil {
				return errLocalInput
			}
			current, err := client.FetchConfig(command.Context(), unsetProject, unsetEnvironment, "")
			if err != nil {
				return markMutationRuntime("unset", err)
			}
			result, err := client.MutateConfig(command.Context(), unsetProject, unsetEnvironment, MutationRequest{
				BaseRevision: current.Revision,
				Message:      unsetMessage,
				Operation: MutationOperation{
					Type: "unset",
					Key:  key,
				},
			})
			if err != nil {
				return markMutationRuntime("unset", err)
			}
			return writeCLICommandOutput(stdoutWriter, fmt.Sprintf("revision %d\n", result.Revision))
		},
	}
	unset.Flags().StringVar(&unsetProject, "project", "", "project slug")
	unset.Flags().StringVar(&unsetEnvironment, "env", "", "environment slug")
	unset.Flags().StringVar(&unsetMessage, "message", "", "mutation message")
	_ = unset.MarkFlagRequired("project")
	_ = unset.MarkFlagRequired("env")

	return []*cobra.Command{set, unset}
}

func parseSetArgument(raw string) (string, string, error) {
	key, value, found := strings.Cut(raw, "=")
	if !found || !environmentKeyPattern.MatchString(key) || !utf8.ValidString(value) || len(value) > maxMutationValueBytes {
		return "", "", errLocalInput
	}
	return key, value, nil
}

func validMutationService(value string) bool {
	return strings.TrimSpace(value) == value && utf8.ValidString(value) && len(value) <= maxServiceBytes
}

func validMutationMessage(value string) bool {
	return strings.TrimSpace(value) == value && utf8.ValidString(value) && len(value) <= maxMutationMessageBytes
}
