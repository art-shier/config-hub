package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
)

func extractUpgradeArchive(archive []byte, base, operatingSystem string) ([]byte, error) {
	errInvalid := errors.New("release archive must contain exactly one directory and its regular CLI executable")
	directory := base + "/"
	executable := directory + "confighub"
	var binary []byte
	seenDirectory, seenExecutable := false, false
	if operatingSystem == "windows" {
		executable += ".exe"
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil || len(reader.File) != 2 {
			return nil, errInvalid
		}
		for _, entry := range reader.File {
			switch {
			case entry.Name == directory && entry.FileInfo().IsDir() && entry.UncompressedSize64 == 0 && !seenDirectory:
				seenDirectory = true
			case entry.Name == executable && entry.Mode().IsRegular() && !seenExecutable && entry.UncompressedSize64 <= maxUpgradeBinaryBytes:
				seenExecutable = true
				stream, err := entry.Open()
				if err != nil {
					return nil, errInvalid
				}
				binary, err = readUpgradeBinary(stream)
				stream.Close()
				if err != nil {
					return nil, err
				}
			default:
				return nil, errInvalid
			}
		}
	} else {
		compressed, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, errInvalid
		}
		defer compressed.Close()
		reader := tar.NewReader(io.LimitReader(compressed, maxUpgradeBinaryBytes+(1<<20)))
		for {
			entry, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errInvalid
			}
			switch {
			case entry.Name == directory && entry.Typeflag == tar.TypeDir && entry.Size == 0 && !seenDirectory:
				seenDirectory = true
			case entry.Name == executable && entry.Typeflag == tar.TypeReg && !seenExecutable && entry.Size <= maxUpgradeBinaryBytes:
				seenExecutable = true
				binary, err = readUpgradeBinary(reader)
				if err != nil {
					return nil, err
				}
			default:
				return nil, errInvalid
			}
		}
	}
	if !seenDirectory || !seenExecutable {
		return nil, errInvalid
	}
	return binary, nil
}

func readUpgradeBinary(reader io.Reader) ([]byte, error) {
	binary, err := io.ReadAll(io.LimitReader(reader, maxUpgradeBinaryBytes+1))
	if err != nil || len(binary) == 0 || len(binary) > maxUpgradeBinaryBytes {
		return nil, errors.New("invalid release executable contents")
	}
	return binary, nil
}
