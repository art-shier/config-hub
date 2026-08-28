package httpapi

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateStrictJSONEnforcesNestingDepth(t *testing.T) {
	boundary := strings.Repeat("[", maxJSONNestingDepth) + strings.Repeat("]", maxJSONNestingDepth)
	if err := validateStrictJSON([]byte(boundary)); err != nil {
		t.Fatalf("boundary JSON rejected: %v", err)
	}

	tooDeep := strings.Repeat("[", maxJSONNestingDepth+1) + strings.Repeat("]", maxJSONNestingDepth+1)
	if err := validateStrictJSON([]byte(tooDeep)); !errors.Is(err, errInvalidJSON) {
		t.Fatalf("too-deep JSON error=%v", err)
	}
}
