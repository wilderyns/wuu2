package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var wuu2StateMu sync.RWMutex
var snapshotUpdateMu sync.Mutex
var snapshotFilePath string
var snapshotLoadOnce sync.Once
var snapshotLoadErr error

func configureSnapshotFile(path string) {
	snapshotFilePath = strings.TrimSpace(path)
}

func snapshotFilePathForDirectory(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "/tmp/wuu2"
	}
	return filepath.Join(dir, "snapshot.json")
}

func tokenFilePathForDirectory(dir string, provider string) string {
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

func ensureSnapshotLoadedFromDisk() error {
	snapshotLoadOnce.Do(func() {
		path := strings.TrimSpace(snapshotFilePath)
		if path == "" {
			return
		}

		snapshot, err := readWuu2Snapshot(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			snapshotLoadErr = err
			return
		}

		if hasWuu2Data(snapshot) {
			setCurrentWuu2Snapshot(snapshot)
		}
	})

	return snapshotLoadErr
}

func getCurrentWuu2Snapshot() Wuu2 {
	wuu2StateMu.RLock()
	defer wuu2StateMu.RUnlock()
	return copyWuu2Snapshot(WUU2)
}

func setCurrentWuu2Snapshot(snapshot Wuu2) {
	wuu2StateMu.Lock()
	defer wuu2StateMu.Unlock()
	WUU2 = copyWuu2Snapshot(snapshot)
}

func persistWuu2Snapshot(path string, snapshot Wuu2) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	return writeFileAtomically(path, payload, 0o644)
}

func readWuu2Snapshot(path string) (Wuu2, error) {
	var snapshot Wuu2

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

func hasWuu2Data(snapshot Wuu2) bool {
	return len(snapshot.Trakt) > 0 ||
		len(snapshot.Wow) > 0 ||
		len(snapshot.AppleMusic) > 0 ||
		len(snapshot.Spotify) > 0 ||
		len(snapshot.Steam) > 0
}

func copyWuu2Snapshot(src Wuu2) Wuu2 {
	dst := Wuu2{}

	if len(src.Trakt) > 0 {
		dst.Trakt = append([]Trakt(nil), src.Trakt...)
	}
	if len(src.Wow) > 0 {
		dst.Wow = append([]Wow(nil), src.Wow...)
	}
	if len(src.AppleMusic) > 0 {
		dst.AppleMusic = append([]AppleMusic(nil), src.AppleMusic...)
	}
	if len(src.Spotify) > 0 {
		dst.Spotify = append([]Spotify(nil), src.Spotify...)
	}
	if len(src.Steam) > 0 {
		dst.Steam = append([]Steam(nil), src.Steam...)
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

	if err := tmp.Chmod(mode); err != nil {
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

	return os.Chmod(path, mode)
}
