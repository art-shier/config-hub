package cli

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

var environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func EncodeDotenv(values map[string]string) (string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !environmentKeyPattern.MatchString(key) {
			return "", errors.New("invalid environment variable name")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var output strings.Builder
	for _, key := range keys {
		output.WriteString(key)
		output.WriteString("='")
		output.WriteString(strings.ReplaceAll(values[key], "'", "'\\''"))
		output.WriteString("'\n")
	}
	return output.String(), nil
}
