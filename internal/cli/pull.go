package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var errPullFileExists = errors.New("destination file already exists; use --force to replace it")

func newPullCommand(resolveConfig func() (configSnapshot, error), stdout io.Writer) *cobra.Command {
	var project, environment, service, directory, format, filename string
	var force bool
	command := &cobra.Command{
		Use:   "pull",
		Short: "Pull configuration into a local file for application startup",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			defaultName, valid := pullFilename(format)
			if !valid || directory == "" || !slugPattern.MatchString(project) || !slugPattern.MatchString(environment) || !validService(service) {
				return errLocalInput
			}
			if command.Flags().Changed("filename") {
				if !validPullFilename(filename) {
					return errLocalInput
				}
			} else {
				filename = defaultName
			}
			target := filepath.Join(directory, filename)
			if !force {
				if _, err := os.Lstat(target); err == nil {
					return markMutationRuntime("pull", errPullFileExists)
				} else if !errors.Is(err, os.ErrNotExist) {
					return markMutationRuntime("pull", err)
				}
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
			response, err := client.FetchConfig(command.Context(), project, environment, service)
			if err != nil {
				return markMutationRuntime("pull", err)
			}
			output, err := encodePull(format, response.Values)
			if err != nil {
				return markMutationRuntime("pull", err)
			}
			if err := writePullFile(target, output, force); err != nil {
				return markMutationRuntime("pull", err)
			}
			if _, err := fmt.Fprintf(stdout, "wrote %s (revision %d)\n", target, response.Revision); err != nil {
				return markMutationRuntime("pull", errOutputWrite)
			}
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", "", "project slug")
	command.Flags().StringVar(&environment, "env", "", "environment slug")
	command.Flags().StringVar(&service, "service", "", "optional service filter")
	command.Flags().StringVar(&directory, "dir", "", "destination directory (created if missing)")
	command.Flags().StringVar(&format, "format", "json", "file format: json, jsonc, env, dotenv, yaml or yml")
	command.Flags().StringVar(&filename, "filename", "", "destination filename (not a path)")
	command.Flags().BoolVar(&force, "force", false, "atomically replace an existing file")
	for _, flag := range []string{"project", "env", "dir"} {
		_ = command.MarkFlagRequired(flag)
	}
	return command
}

func pullFilename(format string) (string, bool) {
	switch format {
	case "json", "jsonc", "yaml", "yml":
		return "config." + format, true
	case "env", "dotenv":
		return ".env", true
	default:
		return "", false
	}
}

func validPullFilename(filename string) bool {
	return filename != "" && filename != "." && filename != ".." && !strings.ContainsAny(filename, "/\\<>:\"|?*\x00\r\n") && !strings.HasSuffix(filename, ".") && !strings.HasSuffix(filename, " ")
}

func encodePull(format string, values map[string]string) ([]byte, error) {
	switch format {
	case "json", "jsonc":
		output, err := json.MarshalIndent(values, "", "  ")
		if err != nil {
			return nil, errExportEncoding
		}
		return append(output, '\n'), nil
	case "yaml", "yml":
		output, err := yaml.Marshal(values)
		if err != nil {
			return nil, errExportEncoding
		}
		return output, nil
	case "env", "dotenv":
		keys := make([]string, 0, len(values))
		for key, value := range values {
			if !environmentKeyPattern.MatchString(key) || !validEnvironmentValue(value) {
				return nil, errExportEncoding
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r")
		var output strings.Builder
		for _, key := range keys {
			fmt.Fprintf(&output, "%s=\"%s\"\n", key, replacer.Replace(values[key]))
		}
		return []byte(output.String()), nil
	default:
		return nil, errExportEncoding
	}
}

func writePullFile(target string, output []byte, force bool) error {
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".confighub-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(output); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(temporary.Name(), target)
	}
	if err := os.Link(temporary.Name(), target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errPullFileExists
		}
		return err
	}
	return nil
}
