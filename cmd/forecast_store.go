package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const forecastStoreLimit = 4096

type storedForecast struct {
	Expires time.Time
	Body    []byte
}

func forecastStorePath(url string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find forecast cache: %w", err)
	}
	return filepath.Join(root, "weather", "forecasts-v1", fmt.Sprintf("%x.json", sha256.Sum256([]byte(url)))), nil
}

func readStoredForecast(path string, now time.Time) (storedForecast, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Debug("read forecast cache", "err", err)
		}
		return storedForecast{}, false
	}
	var entry storedForecast
	if err := json.Unmarshal(body, &entry); err != nil {
		slog.Debug("decode forecast cache", "err", err)
		return storedForecast{}, false
	}
	if !now.Before(entry.Expires) || !json.Valid(entry.Body) {
		return storedForecast{}, false
	}
	return entry, true
}

func writeStoredForecast(path string, entry storedForecast) error {
	if !json.Valid(entry.Body) {
		return fmt.Errorf("forecast response is not JSON")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create forecast cache: %w", err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode forecast cache: %w", err)
	}
	file, err := os.CreateTemp(dir, ".forecast-*")
	if err != nil {
		return fmt.Errorf("create forecast cache file: %w", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !os.IsNotExist(err) {
			slog.Debug("remove forecast cache temporary file", "err", err)
		}
	}()
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write forecast cache: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close forecast cache: %w", closeErr)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("save forecast cache: %w", err)
	}
	return pruneForecastStore(dir)
}

func pruneForecastStore(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list forecast cache: %w", err)
	}
	// Lexical hash order spreads eviction across locations without another index.
	excess := len(entries) - forecastStoreLimit
	for _, entry := range entries {
		if excess <= 0 {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune forecast cache: %w", err)
		}
		excess--
	}
	return nil
}
