package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

func TestEncodeDotenvRejectsNUL(t *testing.T) {
	if output, err := EncodeDotenv(map[string]string{"KEY": "before\x00after"}); err == nil || output != "" {
		t.Fatalf("output=%q error=%v", output, err)
	}
}

func TestEncodeDotenvPreservesCarriageReturnAndUnicodeRoundTrip(t *testing.T) {
	values := map[string]string{
		"CARRIAGE": "before\rafter",
		"UNICODE":  "雪-😃",
	}
	encoded, err := EncodeDotenv(values)
	if err != nil {
		t.Fatal(err)
	}
	want := "CARRIAGE='before\rafter'\nUNICODE='雪-😃'\n"
	if !bytes.Equal([]byte(encoded), []byte(want)) {
		t.Fatalf("bytes=% x want=% x", []byte(encoded), []byte(want))
	}
	path := filepath.Join(t.TempDir(), "values.env")
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", `. "$1"; printf '%s\000%s' "$CARRIAGE" "$UNICODE"`, "sh", path)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := append([]byte(values["CARRIAGE"]), 0)
	wantOutput = append(wantOutput, []byte(values["UNICODE"])...)
	if !bytes.Equal(output, wantOutput) {
		t.Fatalf("round-trip bytes=% x want=% x", output, wantOutput)
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
