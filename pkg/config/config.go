package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	DefaultRelayURL = "wss://relay.wormhole.bar/_wormhole/register"
	DefaultRelay    = "wormhole.bar"
	GitHubClientID  = "Ov23liOLDTqnWJyadktb"
	CallbackURL     = "https://relay.wormhole.bar/_wormhole/auth/callback"
)

// UserConfig holds persistent user configuration stored in ~/.wormhole/config.json.
type UserConfig struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
}

// ConfigDir returns the path to ~/.wormhole/ and creates it if needed.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".wormhole")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// ConfigPath returns the full path to the config file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the user config from disk. Returns empty config if file doesn't exist.
func Load() (*UserConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserConfig{}, nil
		}
		return nil, err
	}

	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &UserConfig{}, nil
	}
	return &cfg, nil
}

// Save writes the user config to disk.
func (c *UserConfig) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
