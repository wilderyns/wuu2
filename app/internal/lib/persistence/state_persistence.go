package persistence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"wuu2/internal/model"
)

type SnapshotStore struct {
	mu               sync.RWMutex
	snapshot         model.Wuu2
	snapshotFilePath string
	loadOnce         sync.Once
	loadErr          error
}

func NewSnapshotStore(path string) *SnapshotStore {
	return &SnapshotStore{snapshotFilePath: strings.TrimSpace(path)}
}

func SnapshotFilePathForDirectory(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "/tmp/wuu2"
	}
	return filepath.Join(dir, "snapshot.json")
}

func TokenFilePathForDirectory(dir string, provider string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "/tmp/wuu2"
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "provider"
	}

	return filepath.Join(dir, "tokens", provider+".json")
}

func (s *SnapshotStore) EnsureLoadedFromDisk() error {
	s.loadOnce.Do(func() {
		path := strings.TrimSpace(s.snapshotFilePath)
		if path == "" {
			return
		}

		snapshot, err := readWuu2Snapshot(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			s.loadErr = err
			return
		}

		if HasWuu2Data(snapshot) {
			s.Set(snapshot)
		}
	})

	return s.loadErr
}

func (s *SnapshotStore) Get() model.Wuu2 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyWuu2Snapshot(s.snapshot)
}

func (s *SnapshotStore) Set(snapshot model.Wuu2) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = copyWuu2Snapshot(snapshot)
}

func (s *SnapshotStore) Persist(snapshot model.Wuu2) error {
	path := strings.TrimSpace(s.snapshotFilePath)
	if path == "" {
		return nil
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	return writeFileAtomically(path, payload, 0o644)
}

func (s *SnapshotStore) PersistCurrent() error {
	return s.Persist(s.Get())
}

func HasWuu2Data(snapshot model.Wuu2) bool {
	return len(snapshot.Trakt) > 0 ||
		len(snapshot.Wow) > 0 ||
		len(snapshot.AppleMusic) > 0 ||
		len(snapshot.Spotify) > 0 ||
		len(snapshot.Steam) > 0
}

func readWuu2Snapshot(path string) (model.Wuu2, error) {
	var snapshot model.Wuu2

	path = strings.TrimSpace(path)
	if path == "" {
		return snapshot, nil
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}

	if len(payload) == 0 {
		return snapshot, nil
	}

	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return snapshot, err
	}

	return snapshot, nil
}

func copyWuu2Snapshot(src model.Wuu2) model.Wuu2 {
	dst := model.Wuu2{}

	if len(src.Trakt) > 0 {
		dst.Trakt = append([]model.Trakt(nil), src.Trakt...)
	}
	if len(src.Wow) > 0 {
		dst.Wow = append([]model.Wow(nil), src.Wow...)
	}
	if len(src.AppleMusic) > 0 {
		dst.AppleMusic = append([]model.AppleMusic(nil), src.AppleMusic...)
	}
	if len(src.Spotify) > 0 {
		dst.Spotify = append([]model.Spotify(nil), src.Spotify...)
	}
	if len(src.Steam) > 0 {
		dst.Steam = append([]model.Steam(nil), src.Steam...)
	}

	return dst
}

func writeFileAtomically(path string, payload []byte, mode os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)
	dirMode := os.FileMode(0o755)
	if mode&0o077 == 0 {
		dirMode = 0o700
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(mode); err != nil && !isIgnorableChmodError(err) {
		_ = tmp.Close()
		return err
	}

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	if err := os.Chmod(path, mode); err != nil && !isIgnorableChmodError(err) {
		return err
	}

	return nil
}

func isIgnorableChmodError(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}
