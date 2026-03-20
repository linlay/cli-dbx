package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultConfigDir  = ".dbx"
	defaultConfigFile = "config.toml"
)

type Config struct {
	DefaultConnection string                      `toml:"default_connection"`
	Connections       map[string]ConnectionConfig `toml:"connections"`
}

type ConnectionConfig struct {
	Engine   string      `toml:"engine"`
	DSN      string      `toml:"dsn"`
	DSNEnv   string      `toml:"dsn_env"`
	Host     string      `toml:"host"`
	Port     int         `toml:"port"`
	User     string      `toml:"user"`
	Password ValueSource `toml:"password"`
	Database string      `toml:"database"`
	Schema   string      `toml:"schema"`
	Path     string      `toml:"path"`
	SSLMode  string      `toml:"sslmode"`
	ReadOnly bool        `toml:"readonly"`
	Timeout  string      `toml:"timeout"`
	Role     string      `toml:"role"`
	Mode     string      `toml:"mode"`
	Tags     []string    `toml:"tags"`
}

type ValueSource struct {
	Value string
	Env   string
	Cmd   []string
	File  string
}

func (v *ValueSource) UnmarshalTOML(input any) error {
	switch raw := input.(type) {
	case string:
		v.Value = raw
		return nil
	case map[string]any:
		for key, value := range raw {
			switch key {
			case "env":
				s, ok := value.(string)
				if !ok {
					return fmt.Errorf("env source must be a string")
				}
				v.Env = s
			case "file":
				s, ok := value.(string)
				if !ok {
					return fmt.Errorf("file source must be a string")
				}
				v.File = s
			case "cmd":
				items, ok := value.([]any)
				if !ok {
					return fmt.Errorf("cmd source must be an array")
				}
				cmd := make([]string, 0, len(items))
				for _, item := range items {
					s, ok := item.(string)
					if !ok {
						return fmt.Errorf("cmd items must be strings")
					}
					cmd = append(cmd, s)
				}
				v.Cmd = cmd
			default:
				return fmt.Errorf("unknown source key %q", key)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported value source type %T", input)
	}
}

func Load(path string) (*Config, string, error) {
	configPath, err := resolvePath(path)
	if err != nil {
		return nil, "", err
	}
	buf, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{Connections: map[string]ConnectionConfig{}}, configPath, nil
		}
		return nil, "", err
	}
	var cfg Config
	if _, err := toml.Decode(string(buf), &cfg); err != nil {
		return nil, "", err
	}
	if cfg.Connections == nil {
		cfg.Connections = map[string]ConnectionConfig{}
	}
	return &cfg, configPath, nil
}

func resolvePath(path string) (string, error) {
	if path != "" {
		return expandHome(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultConfigDir, defaultConfigFile), nil
}

func expandHome(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func (c ConnectionConfig) EffectiveTimeout() time.Duration {
	if c.Timeout == "" {
		return 15 * time.Second
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 15 * time.Second
	}
	return d
}

func (v ValueSource) Resolve(ctx context.Context) (string, string, error) {
	if v.Value != "" {
		return v.Value, "value", nil
	}
	if v.Env != "" {
		s, ok := os.LookupEnv(v.Env)
		if !ok {
			return "", "env", fmt.Errorf("environment variable %s is not set", v.Env)
		}
		return s, "env", nil
	}
	if v.File != "" {
		path, err := expandHome(v.File)
		if err != nil {
			return "", "file", err
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			return "", "file", err
		}
		return strings.TrimRight(string(buf), "\r\n"), "file", nil
	}
	if len(v.Cmd) > 0 {
		if strings.Contains(v.Cmd[0], " ") {
			return "", "cmd", fmt.Errorf("cmd source must be an argv array, not a shell string")
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, v.Cmd[0], v.Cmd[1:]...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", "cmd", fmt.Errorf("command %q failed: %w: %s", v.Cmd[0], err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimRight(stdout.String(), "\r\n"), "cmd", nil
	}
	return "", "", nil
}
