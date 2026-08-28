package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	errExportEncoding = errors.New("export encoding failed")
	errOutputWrite    = errors.New("stdout write failed")
)

func WriteExport(w io.Writer, format string, response ConfigResponse) error {
	var output []byte
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return fmt.Errorf("%w: %w", errExportEncoding, err)
		}
		output = append(encoded, '\n')
	case "dotenv":
		encoded, err := EncodeDotenv(response.Values)
		if err != nil {
			return fmt.Errorf("%w: %w", errExportEncoding, err)
		}
		output = []byte(encoded)
	default:
		return errExportEncoding
	}

	written, err := w.Write(output)
	if err != nil {
		return fmt.Errorf("%w: %w", errOutputWrite, err)
	}
	if written != len(output) {
		return fmt.Errorf("%w: %w", errOutputWrite, io.ErrShortWrite)
	}
	return nil
}
