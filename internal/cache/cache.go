package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const cacheFileName = ".deployd_state.json"
const cacheFileDirectory = ".deployd"

var cache = make(map[string]string)

func LoadCache() error {
	cachePath, err := getCacheFilePath()
	if err != nil {
		return err
	}

	if _, err = os.Stat(cachePath); err == nil {
		data, err := os.ReadFile(cachePath)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &cache)
	} else if os.IsNotExist(err) {

		if err = os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			return err
		}

		cacheJsonByte, err := json.Marshal(cache)

		if err != nil {
			return err
		}

		return os.WriteFile(cachePath, cacheJsonByte, 0644)
	}

	return err
}

func Get(key string) string {
	return cache[key]
}

func Set(key string, value string) error {
	cache[key] = value

	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	cachePath, err := getCacheFilePath()
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0644)
}

func getCacheFilePath() (path string, err error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome != "" {
		return filepath.Join(configHome, cacheFileDirectory, cacheFileName), nil
	}

	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}

	return filepath.Join(home, cacheFileDirectory, cacheFileName), nil
}
