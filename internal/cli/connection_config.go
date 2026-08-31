package cli

import "github.com/spf13/cobra"

func resolveConnectionConfig(
	root *cobra.Command,
	snapshot configSnapshot,
	serverFlag string,
	tokenFile string,
	getenv func(string) string,
) (configSnapshot, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	if root.PersistentFlags().Lookup("server").Changed {
		if _, err := validateBaseURL(serverFlag); err != nil {
			return configSnapshot{}, errLocalInput
		}
		snapshot.Server = configValue{
			Value: serverFlag, Present: true, Source: configSource{Kind: sourceServerFlag},
		}
	} else if value := getenv("CONFIGHUB_URL"); value != "" {
		if _, err := validateBaseURL(value); err != nil {
			return configSnapshot{}, errLocalInput
		}
		snapshot.Server = configValue{
			Value: value, Present: true, Source: configSource{Kind: sourceEnvironment},
		}
	}

	if root.PersistentFlags().Lookup("token-file").Changed {
		token, err := readTokenFile(tokenFile)
		if err != nil {
			return configSnapshot{}, errLocalInput
		}
		snapshot.Token = configValue{
			Value: token, Present: true, Source: configSource{Kind: sourceTokenFile},
		}
	} else if value := getenv("CONFIGHUB_TOKEN"); value != "" {
		if !validToken(value) {
			return configSnapshot{}, errLocalInput
		}
		snapshot.Token = configValue{
			Value: value, Present: true, Source: configSource{Kind: sourceEnvironment},
		}
	}
	return snapshot, nil
}

func requireConnectionConfig(snapshot configSnapshot) (string, string, error) {
	if !snapshot.Server.Present || !snapshot.Token.Present {
		return "", "", errLocalInput
	}
	if _, err := validateBaseURL(snapshot.Server.Value); err != nil || !validToken(snapshot.Token.Value) {
		return "", "", errLocalInput
	}
	return snapshot.Server.Value, snapshot.Token.Value, nil
}
