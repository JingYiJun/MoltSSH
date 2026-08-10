package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve_PrefersInjectedValues_whenReleaseMetadataIsProvided(t *testing.T) {
	info := resolve(
		"v1.2.3",
		"abcdef1234567890",
		"go1.26.5",
		&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0"}},
	)

	if info.Version != "v1.2.3" || info.Commit != "abcdef1234567890" || info.GoVersion != "go1.26.5" {
		t.Fatalf("unexpected build info: %+v", info)
	}
}

func TestResolve_UsesGoBuildInfo_whenInjectedValuesAreMissing(t *testing.T) {
	info := resolve(
		"",
		"",
		"go1.26.5",
		&debug.BuildInfo{
			Main: debug.Module{Version: "v4.5.6"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef"},
			},
		},
	)

	if info.Version != "v4.5.6" || info.Commit != "0123456789abcdef" || info.GoVersion != "go1.26.5" {
		t.Fatalf("unexpected build info: %+v", info)
	}
}

func TestResolve_UsesDevelopmentDefaults_whenMetadataIsUnavailable(t *testing.T) {
	info := resolve("", "", "go1.26.5", nil)

	if info.Version != "dev" || info.Commit != "unknown" || info.GoVersion != "go1.26.5" {
		t.Fatalf("unexpected build info: %+v", info)
	}
}
