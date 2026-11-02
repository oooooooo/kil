package main

import (
	"bytes"
	"strings"
	"testing"
)

func runKil(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	getenv := func(k string) string { return env[k] }
	code := run(args, getenv, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		code, out, _ := runKil(t, nil, arg)
		if code != 0 || !strings.Contains(out, "Usage: kil") {
			t.Errorf("kil %s = %d, %q; want 0 and usage", arg, code, out)
		}
	}
}

func TestUnknownOptionIsAUsageError(t *testing.T) {
	code, _, errOut := runKil(t, nil, "-x")
	if code != 2 || !strings.Contains(errOut, "kil -- -x") {
		t.Errorf("kil -x = %d, %q; want 2 and a hint to use --", code, errOut)
	}
}

func TestInvalidSignalIsRejectedBeforeListing(t *testing.T) {
	code, _, errOut := runKil(t, map[string]string{"KIL_SIGNAL": "-9"})
	if code != 2 || !strings.Contains(errOut, "KIL_SIGNAL") {
		t.Errorf("KIL_SIGNAL=-9 kil = %d, %q; want 2 and a KIL_SIGNAL error", code, errOut)
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args  []string
		query string
	}{
		{nil, ""},
		{[]string{"node"}, "node"},
		{[]string{"node", "dev"}, "node dev"},
		{[]string{"--", "-zsh"}, "-zsh"},
		{[]string{"--", "--help"}, "--help"},
		{[]string{"--", "-e"}, "-e"},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil || got.query != tt.query || got.help || got.exact {
			t.Errorf("parseArgs(%q) = %+v, %v; want query %q", tt.args, got, err, tt.query)
		}
	}
}

func TestExactOption(t *testing.T) {
	for _, args := range [][]string{{"-e", "ruby"}, {"--exact", "ruby"}, {"-e", "--", "ruby"}} {
		got, err := parseArgs(args)
		if err != nil || !got.exact || got.query != "ruby" {
			t.Errorf("parseArgs(%q) = %+v, %v; want exact with query \"ruby\"", args, got, err)
		}
	}
}

func TestExactFromEnvironment(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{"true", true},
	}
	for _, tt := range tests {
		got, err := exactFromEnv(tt.value)
		if err != nil || got != tt.want {
			t.Errorf("exactFromEnv(%q) = %v, %v; want %v", tt.value, got, err, tt.want)
		}
	}
	if _, err := exactFromEnv("yes please"); err == nil {
		t.Error("exactFromEnv(\"yes please\") succeeded, want an error")
	}
}

func TestInvalidExactEnvironmentIsRejected(t *testing.T) {
	code, _, errOut := runKil(t, map[string]string{"KIL_EXACT": "maybe"})
	if code != 2 || !strings.Contains(errOut, "KIL_EXACT") {
		t.Errorf("KIL_EXACT=maybe kil = %d, %q; want 2 and a KIL_EXACT error", code, errOut)
	}
}
