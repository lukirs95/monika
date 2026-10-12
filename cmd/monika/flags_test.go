package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlags(t *testing.T) {
	var out bytes.Buffer
	if code := run(t.Context(), []string{"-version"}, nil, &out, &out); code != 0 || !strings.HasPrefix(out.String(), "monika ") {
		t.Errorf("-version = %d %q", code, out.String())
	}
	out.Reset()
	if code := run(t.Context(), nil, []string{"MONIKA_ENV_FILE=/nonexistent"}, &out, &out); code != 1 || !strings.Contains(out.String(), "MONIKA_DATABASE_URL") {
		t.Errorf("without configuration = %d %q", code, out.String())
	}
}
