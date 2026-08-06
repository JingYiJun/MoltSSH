package tunnel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPathState_RoundTripsPathNameOnly(t *testing.T) {
	// Given
	store := newTestPathStateStore(t, "one.toml")
	cfg := &Config{Paths: []PathConfig{{
		Name:     "direct",
		Enabled:  true,
		Endpoint: "wss://example.invalid/moltssh?token=query-secret",
	}}}

	// When
	err := store.Save(cfg, "direct")
	loaded, loadErr := store.Load(cfg)

	// Then
	if err != nil || loadErr != nil || loaded == nil || loaded.Name != "direct" {
		t.Fatalf("save=%v load=%v path=%+v", err, loadErr, loaded)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"version":1,"path":"direct"}`; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("file mode = %o, want %o", got, want)
	}
	dirInfo, err := os.Stat(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dirInfo.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Fatalf("directory mode = %o, want %o", got, want)
	}
	for _, forbidden := range []string{"endpoint", "query-secret", "session-secret", "offset-secret", "payload-secret"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("state contains sensitive fixture data %q", forbidden)
		}
	}
}

func TestPathState_LoadIgnoresAbsentAndStalePath(t *testing.T) {
	tests := []struct {
		name string
		data string
		cfg  *Config
	}{
		{name: "absent", cfg: pathStateConfig(pathStateTestPath{"direct", true})},
		{name: "removed", data: `{"version":1,"path":"removed"}`, cfg: pathStateConfig(pathStateTestPath{"direct", true})},
		{name: "disabled", data: `{"version":1,"path":"direct"}`, cfg: pathStateConfig(pathStateTestPath{"direct", false})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			store := newTestPathStateStore(t, test.name+".toml")
			if test.data != "" {
				writePathStateFixture(t, store.path, test.data)
			}

			// When
			path, err := store.Load(test.cfg)

			// Then
			if err != nil || path != nil {
				t.Fatalf("load = (%+v, %v), want (nil, nil)", path, err)
			}
		})
	}
}

func TestPathState_IgnoresCorruptState(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "malformed", data: `{"version":`},
		{name: "unknown version", data: `{"version":2,"path":"direct"}`},
		{name: "unexpected field", data: `{"version":1,"path":"direct","endpoint":"endpoint-secret"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			store := newTestPathStateStore(t, test.name+".toml")
			writePathStateFixture(t, store.path, test.data)

			// When
			path, err := store.Load(pathStateConfig(pathStateTestPath{"direct", true}))

			// Then
			var advisory *PathStateError
			if path != nil || !errors.As(err, &advisory) {
				t.Fatalf("load = (%+v, %v), want advisory error", path, err)
			}
		})
	}
}

func TestPathState_IsolatesConfigs(t *testing.T) {
	// Given
	root := t.TempDir()
	first := newPathStateStore(root, filepath.Join(root, "first.toml"))
	second := newPathStateStore(root, filepath.Join(root, "second.toml"))
	cfg := pathStateConfig(pathStateTestPath{"direct", true})

	// When
	err := first.Save(cfg, "direct")
	path, loadErr := second.Load(cfg)

	// Then
	if err != nil || loadErr != nil || path != nil || first.path == second.path {
		t.Fatalf("save=%v load=(%+v, %v) first=%q second=%q", err, path, loadErr, first.path, second.path)
	}
}

func TestPathState_SaveDoesNotRewriteSamePath(t *testing.T) {
	// Given
	store := newTestPathStateStore(t, "same.toml")
	cfg := pathStateConfig(pathStateTestPath{"direct", true})
	if err := store.Save(cfg, "direct"); err != nil {
		t.Fatal(err)
	}
	historical := time.Unix(1, 0)
	if err := os.Chtimes(store.path, historical, historical); err != nil {
		t.Fatal(err)
	}

	// When
	err := store.Save(cfg, "direct")

	// Then
	info, statErr := os.Stat(store.path)
	if err != nil || statErr != nil || !info.ModTime().Equal(historical) {
		t.Fatalf("save=%v stat=%v modified=%v", err, statErr, info.ModTime())
	}
}

func TestPathState_WriteFailureIsAdvisory(t *testing.T) {
	// Given
	store := newPathStateStore("/proc", "one.toml")

	// When
	err := store.Save(pathStateConfig(pathStateTestPath{"direct", true}), "direct")

	// Then
	var advisory *PathStateError
	if !errors.As(err, &advisory) {
		t.Fatalf("save error = %v, want advisory error", err)
	}
}

func TestPathState_SerializesConcurrentReadAndWrite(t *testing.T) {
	// Given
	store := newTestPathStateStore(t, "race.toml")
	cfg := pathStateConfig(pathStateTestPath{"first", true}, pathStateTestPath{"second", true})
	if err := store.Save(cfg, "first"); err != nil {
		t.Fatal(err)
	}

	// When
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			name := "first"
			if index%2 == 1 {
				name = "second"
			}
			if err := store.Save(cfg, name); err != nil {
				t.Errorf("save %q: %v", name, err)
			}
			path, err := store.Load(cfg)
			if err != nil || path == nil || (path.Name != "first" && path.Name != "second") {
				t.Errorf("load = (%+v, %v)", path, err)
			}
		}(i)
	}
	group.Wait()

	// Then
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"version":1,"path":"first"}` && got != `{"version":1,"path":"second"}` {
		t.Fatalf("incomplete state record %q", got)
	}
}

func newTestPathStateStore(t *testing.T, source string) *pathStateStore {
	t.Helper()
	return newPathStateStore(t.TempDir(), source)
}

func writePathStateFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type pathStateTestPath struct {
	name    string
	enabled bool
}

func pathStateConfig(paths ...pathStateTestPath) *Config {
	cfg := &Config{}
	for _, path := range paths {
		cfg.Paths = append(cfg.Paths, PathConfig{Name: path.name, Enabled: path.enabled})
	}
	return cfg
}
