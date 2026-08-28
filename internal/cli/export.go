package cli

import (
	"encoding/json"
	"errors"
	"io"
)

func WriteExport(w io.Writer, format string, response ConfigResponse) error {
	var output []byte
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return errors.New("could not encode JSON export")
		}
		output = append(encoded, '\n')
	case "dotenv":
		encoded, err := EncodeDotenv(response.Values)
		if err != nil {
			return err
		}
		output = []byte(encoded)
	default:
		return errors.New("unsupported export format")
	}

	written, err := w.Write(output)
	if err != nil {
		return err
	}
	if written != len(output) {
		return io.ErrShortWrite
	}
	return nil
}
