package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxJSONNestingDepth = 256

var errInvalidJSON = errors.New("invalid JSON request")

func readStrictJSONBody(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maxRequestBodyBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxRequestBodyBytes {
		return nil, &http.MaxBytesError{Limit: maxRequestBodyBytes}
	}
	return body, nil
}

func validateStrictJSON(body []byte) error {
	if !utf8.Valid(body) || !json.Valid(body) {
		return errInvalidJSON
	}
	scanner := strictJSONScanner{body: body}
	if err := scanner.value(0); err != nil {
		return errInvalidJSON
	}
	scanner.skipSpace()
	if scanner.offset != len(scanner.body) {
		return errInvalidJSON
	}
	return nil
}

type strictJSONScanner struct {
	body   []byte
	offset int
}

func (s *strictJSONScanner) value(depth int) error {
	if depth > maxJSONNestingDepth {
		return errInvalidJSON
	}
	s.skipSpace()
	if s.offset >= len(s.body) {
		return errInvalidJSON
	}
	switch s.body[s.offset] {
	case '{':
		if depth >= maxJSONNestingDepth {
			return errInvalidJSON
		}
		return s.object(depth)
	case '[':
		if depth >= maxJSONNestingDepth {
			return errInvalidJSON
		}
		return s.array(depth)
	case '"':
		end, err := strictJSONStringEnd(s.body, s.offset)
		if err != nil {
			return err
		}
		s.offset = end
		return nil
	default:
		start := s.offset
		for s.offset < len(s.body) && !isJSONValueDelimiter(s.body[s.offset]) {
			s.offset++
		}
		if s.offset == start {
			return errInvalidJSON
		}
		return nil
	}
}

func (s *strictJSONScanner) object(depth int) error {
	s.offset++
	s.skipSpace()
	if s.consume('}') {
		return nil
	}
	seen := make(map[string]struct{})
	for {
		s.skipSpace()
		if s.offset >= len(s.body) || s.body[s.offset] != '"' {
			return errInvalidJSON
		}
		start := s.offset
		end, err := strictJSONStringEnd(s.body, start)
		if err != nil {
			return err
		}
		var key string
		if err := json.Unmarshal(s.body[start:end], &key); err != nil {
			return errInvalidJSON
		}
		folded := foldJSONName(key)
		if _, duplicate := seen[folded]; duplicate {
			return errInvalidJSON
		}
		seen[folded] = struct{}{}
		s.offset = end
		s.skipSpace()
		if !s.consume(':') {
			return errInvalidJSON
		}
		if err := s.value(depth + 1); err != nil {
			return err
		}
		s.skipSpace()
		if s.consume('}') {
			return nil
		}
		if !s.consume(',') {
			return errInvalidJSON
		}
	}
}

func foldJSONName(name string) string {
	var folded strings.Builder
	folded.Grow(len(name))
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z':
			character -= 'a' - 'A'
		case character >= utf8.RuneSelf:
			for {
				next := unicode.SimpleFold(character)
				if next <= character {
					character = next
					break
				}
				character = next
			}
		}
		folded.WriteRune(character)
	}
	return folded.String()
}

func (s *strictJSONScanner) array(depth int) error {
	s.offset++
	s.skipSpace()
	if s.consume(']') {
		return nil
	}
	for {
		if err := s.value(depth + 1); err != nil {
			return err
		}
		s.skipSpace()
		if s.consume(']') {
			return nil
		}
		if !s.consume(',') {
			return errInvalidJSON
		}
	}
}

func (s *strictJSONScanner) skipSpace() {
	for s.offset < len(s.body) {
		switch s.body[s.offset] {
		case ' ', '\t', '\r', '\n':
			s.offset++
		default:
			return
		}
	}
}

func (s *strictJSONScanner) consume(want byte) bool {
	if s.offset >= len(s.body) || s.body[s.offset] != want {
		return false
	}
	s.offset++
	return true
}

func strictJSONStringEnd(body []byte, start int) (int, error) {
	if start >= len(body) || body[start] != '"' {
		return 0, errInvalidJSON
	}
	for offset := start + 1; offset < len(body); {
		switch body[offset] {
		case '"':
			return offset + 1, nil
		case '\\':
			if offset+1 >= len(body) {
				return 0, errInvalidJSON
			}
			if body[offset+1] != 'u' {
				offset += 2
				continue
			}
			code, ok := jsonHexCodeUnit(body, offset+2)
			if !ok {
				return 0, errInvalidJSON
			}
			switch {
			case code >= 0xd800 && code <= 0xdbff:
				pairOffset := offset + 6
				if pairOffset+6 > len(body) || body[pairOffset] != '\\' || body[pairOffset+1] != 'u' {
					return 0, errInvalidJSON
				}
				low, ok := jsonHexCodeUnit(body, pairOffset+2)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return 0, errInvalidJSON
				}
				offset += 12
			case code >= 0xdc00 && code <= 0xdfff:
				return 0, errInvalidJSON
			default:
				offset += 6
			}
		default:
			offset++
		}
	}
	return 0, errInvalidJSON
}

func jsonHexCodeUnit(body []byte, start int) (uint16, bool) {
	if start+4 > len(body) {
		return 0, false
	}
	var value uint16
	for _, character := range body[start : start+4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func isJSONValueDelimiter(character byte) bool {
	switch character {
	case ' ', '\t', '\r', '\n', ',', ']', '}':
		return true
	default:
		return false
	}
}
