package slogpretty

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestPrettyHandlerKeepsAttrsAcrossWithAndGroup(t *testing.T) {
	var out bytes.Buffer
	log := slog.New((PrettyHandlerOptions{}).NewPrettyHandler(&out))
	log.With("request_id", "123").WithGroup("auth").With("operation", "sign_in").Info("handled", "result", "ok")

	got := out.String()
	for _, want := range []string{`"request_id": "123"`, `"auth": {`, `"operation": "sign_in"`, `"result": "ok"`} {
		if !strings.Contains(got, want) {
			t.Errorf("log output missing %s: %s", want, got)
		}
	}
}
