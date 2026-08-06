package tunnel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const pathStateVersion = 1

var pathStateMu sync.Mutex

// PathStateError reports an advisory cache failure that callers may warn about
// and otherwise ignore.
type PathStateError struct {
	Operation string
	Err       error
}

func (e *PathStateError) Error() string {
	return fmt.Sprintf("path state %s: %v", e.Operation, e.Err)
}

func (e *PathStateError) Unwrap() error {
	return e.Err
}

type pathStateStore struct {
	path string
}

type pathStateRecord struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
}

func LoadLastKnownGoodPath(cfg *Config) (*PathConfig, error) {
	store, err := pathStateStoreForConfig(cfg)
	if err != nil || store == nil {
		return nil, err
	}
	return store.Load(cfg)
}

func SaveLastKnownGoodPath(cfg *Config, name string) error {
	store, err := pathStateStoreForConfig(cfg)
	if err != nil || store == nil {
		return err
	}
	return store.Save(cfg, name)
}

func pathStateStoreForConfig(cfg *Config) (*pathStateStore, error) {
	if cfg == nil || cfg.sourceIdentity == "" {
		return nil, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, newPathStateError("locate cache", err)
	}
	return newPathStateStore(cacheDir, cfg.sourceIdentity), nil
}

func newPathStateStore(cacheDir, sourceIdentity string) *pathStateStore {
	sum := sha256.Sum256([]byte(sourceIdentity))
	filename := hex.EncodeToString(sum[:]) + ".json"
	return &pathStateStore{path: filepath.Join(cacheDir, "moltssh", "path-state", filename)}
}

func (s *pathStateStore) Load(cfg *Config) (*PathConfig, error) {
	pathStateMu.Lock()
	defer pathStateMu.Unlock()

	record, present, err := s.readLocked()
	if err != nil || !present {
		return nil, err
	}
	if record.Version != pathStateVersion {
		return nil, newPathStateError("validate", fmt.Errorf("unsupported version %d", record.Version))
	}
	path := enabledPathByName(cfg, record.Path)
	return path, nil
}

func (s *pathStateStore) Save(cfg *Config, name string) error {
	if enabledPathByName(cfg, name) == nil {
		return newPathStateError("validate", fmt.Errorf("path %q is not enabled", name))
	}
	pathStateMu.Lock()
	defer pathStateMu.Unlock()

	record, present, err := s.readLocked()
	if err == nil && present && record.Version == pathStateVersion && record.Path == name {
		return nil
	}
	return s.writeLocked(pathStateRecord{Version: pathStateVersion, Path: name})
}

func (s *pathStateStore) readLocked() (pathStateRecord, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return pathStateRecord{}, false, nil
	}
	if err != nil {
		return pathStateRecord{}, false, newPathStateError("read", err)
	}
	var record pathStateRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return pathStateRecord{}, false, newPathStateError("decode", err)
	}
	if err := consumeEOF(decoder); err != nil {
		return pathStateRecord{}, false, newPathStateError("decode", err)
	}
	return record, true, nil
}

func (s *pathStateStore) writeLocked(record pathStateRecord) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return newPathStateError("create cache directory", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return newPathStateError("secure cache directory", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return newPathStateError("encode", err)
	}
	temp, err := os.CreateTemp(dir, ".path-state-*")
	if err != nil {
		return newPathStateError("create temporary state", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		return newPathStateError("secure temporary state", closePathStateTemp(temp, err))
	}
	if _, err := temp.Write(data); err != nil {
		return newPathStateError("write", closePathStateTemp(temp, err))
	}
	if err := temp.Sync(); err != nil {
		return newPathStateError("sync", closePathStateTemp(temp, err))
	}
	if err := temp.Close(); err != nil {
		return newPathStateError("close", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return newPathStateError("replace", err)
	}
	return nil
}

func enabledPathByName(cfg *Config, name string) *PathConfig {
	if cfg == nil || name == "" {
		return nil
	}
	for _, path := range cfg.Paths {
		if path.Name == name && path.Enabled {
			result := path
			return &result
		}
	}
	return nil
}

func pathsExcept(paths []PathConfig, excluded string) []PathConfig {
	result := make([]PathConfig, 0, len(paths))
	for _, path := range paths {
		if path.Name != excluded {
			result = append(result, path)
		}
	}
	return result
}

func (rt *clientRuntime) lastKnownGoodPath() *PathConfig {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.lastKnownGood == nil {
		return nil
	}
	path := *rt.lastKnownGood
	return &path
}

func newPathStateError(operation string, err error) *PathStateError {
	return &PathStateError{Operation: operation, Err: err}
}

func consumeEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func closePathStateTemp(temp *os.File, operationErr error) error {
	if closeErr := temp.Close(); closeErr != nil {
		return errors.Join(operationErr, closeErr)
	}
	return operationErr
}
