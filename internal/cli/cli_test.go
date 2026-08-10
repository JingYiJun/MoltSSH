package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jingyijun/moltssh/internal/buildinfo"
)

func TestRun_PrintsRootHelp_whenHelpFlagIsUsed(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"--help"}, nil, &out, &bytes.Buffer{}, testBuildInfo()); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"Commands:", "proxy", "server", "probe", "version", "Security:"} {
		if got := out.String(); !strings.Contains(got, token) {
			t.Fatalf("help output %q is missing %q", got, token)
		}
	}
}

func TestRun_PrintsCommandHelp_whenHelpTopicIsUsed(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"help", "proxy"}, nil, &out, &bytes.Buffer{}, testBuildInfo()); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"Usage:", "moltssh proxy --config FILE", "OpenSSH ProxyCommand"} {
		if got := out.String(); !strings.Contains(got, token) {
			t.Fatalf("proxy help output %q is missing %q", got, token)
		}
	}
}

func TestRun_PrintsCommandHelp_whenSubcommandHelpFlagIsUsed(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"probe", "--help"}, nil, &out, &bytes.Buffer{}, testBuildInfo()); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "moltssh probe --config FILE") {
		t.Fatalf("unexpected probe help output: %q", got)
	}
}

func TestRun_PrintsBuildMetadata_whenVersionCommandIsUsed(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"version"}, nil, &out, &bytes.Buffer{}, testBuildInfo()); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"version: v9.8.7", "commit: abcdef123456", "go: go9.9.9"} {
		if got := out.String(); !strings.Contains(got, token) {
			t.Fatalf("version output %q is missing %q", got, token)
		}
	}
}

func TestRun_ReturnsTroubleshootingHint_whenConfigIsMissing(t *testing.T) {
	err := Run([]string{"proxy"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, testBuildInfo())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, token := range []string{"--config FILE", "moltssh help proxy"} {
		if !strings.Contains(err.Error(), token) {
			t.Fatalf("error %q is missing %q", err, token)
		}
	}
}

func TestServerSecurityWarning_ReturnsWarning_whenListenAddressIsNotLoopback(t *testing.T) {
	warning := serverSecurityWarning("0.0.0.0:8080")
	for _, token := range []string{"no application-layer authentication", "protected reverse proxy"} {
		if !strings.Contains(warning, token) {
			t.Fatalf("warning %q is missing %q", warning, token)
		}
	}
}

func TestServerSecurityWarning_ReturnsEmpty_whenListenAddressIsLoopback(t *testing.T) {
	if warning := serverSecurityWarning("127.0.0.1:8080"); warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
}

func TestRun_ReturnsError_whenCommandIsUnknown(t *testing.T) {
	var stderr bytes.Buffer
	err := Run([]string{"wat"}, nil, &bytes.Buffer{}, &stderr, testBuildInfo())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("missing usage: %q", stderr.String())
	}
}

func testBuildInfo() buildinfo.Info {
	return buildinfo.Info{
		Version:   "v9.8.7",
		Commit:    "abcdef123456",
		GoVersion: "go9.9.9",
	}
}
