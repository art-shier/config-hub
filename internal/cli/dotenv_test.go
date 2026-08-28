package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDotenvSortsAndQuotesWithoutInterpolation(t *testing.T) {
	values := map[string]string{
		"Z_LAST":  "plain",
		"A_FIRST": "line one\nline two\n$(touch /tmp/never)",
	}
	got, err := EncodeDotenv(values)
	if err != nil {
		t.Fatal(err)
	}
	want := "A_FIRST='line one\nline two\n$(touch /tmp/never)'\nZ_LAST='plain'\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEncodeDotenvEscapesSingleQuoteWithExactShellSafeBytes(t *testing.T) {
	got, err := EncodeDotenv(map[string]string{"KEY": "a'b"})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("KEY='a"), 0x27, 0x5c, 0x27, 0x27)
	want = append(want, []byte("b'\n")...)
	if !bytes.Equal([]byte(got), want) {
		t.Fatalf("bytes=% x want=% x", []byte(got), want)
	}
}

func TestEncodeDotenvKeepsShellSyntaxLiteral(t *testing.T) {
	value := "`echo never` $HOME ${USER} $(touch /tmp/never) C:\\path\\file"
	got, err := EncodeDotenv(map[string]string{"VALUE": value})
	if err != nil {
		t.Fatal(err)
	}
	if want := "VALUE='" + value + "'\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEncodeDotenvRejectsUnsafeVariableNames(t *testing.T) {
	for _, key := range []string{"", "1START", "BAD-NAME", "BAD NAME", "A\nINJECT", "A=VALUE", "ÅNGSTROM"} {
		t.Run(strings.ReplaceAll(key, "\n", "newline"), func(t *testing.T) {
			if _, err := EncodeDotenv(map[string]string{key: "secret-value"}); err == nil {
				t.Fatalf("EncodeDotenv accepted key %q", key)
			}
		})
	}
}
