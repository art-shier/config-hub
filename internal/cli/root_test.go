package cli

import (
	"bytes"
	"context"
	"io"
	"testing"

	"confighub.local/internal/buildinfo"
)

func TestExecuteVersionWritesBuildVersionWithoutCredentials(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "v1.2.3"
	t.Cleanup(func() { buildinfo.Version = original })
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), []string{"version"}, nil, &stdout, &stderr)

	if code != 0 || stdout.String() != "v1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteVersionRejectsArgumentsWithoutWritingVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"version", "extra"}, nil, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExecuteVersionReportsStdoutWriteFailure(t *testing.T) {
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"version"}, nil, versionFailingWriter{}, &stderr)
	if code != 1 || stderr.String() != "confighub: stdout write failed\n" {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

type versionFailingWriter struct{}

func (versionFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

var _ io.Writer = versionFailingWriter{}
