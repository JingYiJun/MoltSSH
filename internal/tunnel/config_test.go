package tunnel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFile_AttachesCanonicalIdentityOnlyForProxy(t *testing.T) {
	// Given
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.toml")
	if err := os.WriteFile(realPath, []byte(pathStateConfigTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "link.toml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}

	// When
	proxyConfig, proxyErr := LoadConfigFile(linkPath, CommandProxy)
	serverConfig, serverErr := LoadConfigFile(realPath, CommandServer)
	parsedConfig, parsedErr := ParseConfig(pathStateConfigTOML, CommandProxy)

	// Then
	if proxyErr != nil || serverErr != nil || parsedErr != nil {
		t.Fatalf("proxy=%v server=%v parsed=%v", proxyErr, serverErr, parsedErr)
	}
	want, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if proxyConfig.sourceIdentity != want || serverConfig.sourceIdentity != "" || parsedConfig.sourceIdentity != "" {
		t.Fatalf("identities proxy=%q server=%q parsed=%q want=%q", proxyConfig.sourceIdentity, serverConfig.sourceIdentity, parsedConfig.sourceIdentity, want)
	}
}

const pathStateConfigTOML = `schema_version = 1
name = "loop"

[server]
listen = "127.0.0.1:8080"
connect = "127.0.0.1:22"

[[paths]]
name = "direct"
endpoint = "ws://127.0.0.1:8080/moltssh"
`
