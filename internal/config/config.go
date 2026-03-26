package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const defaultConfigDir = ".config/dbx"

type Profile struct {
	Name       string
	Path       string
	Connection ConnectionConfig
}

type fileConfig struct {
	Connection ConnectionConfig `toml:"connection"`
}

type ConnectionConfig struct {
	Engine       string      `toml:"engine"`
	DSN          string      `toml:"dsn"`
	DSNEnv       string      `toml:"dsn_env"`
	Host         string      `toml:"host"`
	Port         int         `toml:"port"`
	User         string      `toml:"user"`
	Password     ValueSource `toml:"password"`
	Database     string      `toml:"database"`
	Schema       string      `toml:"schema"`
	Path         string      `toml:"path"`
	SSLMode      string      `toml:"sslmode"`
	ReadOnly     bool        `toml:"readonly"`
	Timeout      string      `toml:"timeout"`
	Role         string      `toml:"role"`
	Mode         string      `toml:"mode"`
	Tags         []string    `toml:"tags"`
	AllowActions []string    `toml:"allow_actions"`
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

func List(path string) ([]Profile, string, error) {
	resolvedPath, err := resolvePath(path)
	if err != nil {
		return nil, "", err
	}
	kind, exists, err := detectPathKind(resolvedPath)
	if err != nil {
		return nil, "", err
	}
	if kind == pathKindFile {
		profile, err := loadFile(resolvedPath)
		if err != nil {
			return nil, "", err
		}
		return []Profile{profile}, resolvedPath, nil
	}
	if !exists {
		return []Profile{}, resolvedPath, nil
	}
	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return nil, "", err
	}
	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		profile, err := loadFile(filepath.Join(resolvedPath, entry.Name()))
		if err != nil {
			return nil, "", err
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, resolvedPath, nil
}

func LoadNamed(path, name string) (Profile, error) {
	if strings.TrimSpace(name) == "" {
		return Profile{}, fmt.Errorf("no connection selected")
	}
	resolvedPath, err := resolvePath(path)
	if err != nil {
		return Profile{}, err
	}
	kind, _, err := detectPathKind(resolvedPath)
	if err != nil {
		return Profile{}, err
	}
	if kind == pathKindFile {
		profile, err := loadFile(resolvedPath)
		if err != nil {
			return Profile{}, err
		}
		if profile.Name != name {
			return Profile{}, fmt.Errorf("connection %q does not match config file %q; expected %q", name, resolvedPath, profile.Name)
		}
		return profile, nil
	}
	configFile := filepath.Join(resolvedPath, name+".toml")
	profile, err := loadFile(configFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, fmt.Errorf("connection %q not found; expected %s", name, configFile)
		}
		return Profile{}, err
	}
	return profile, nil
}

func resolvePath(path string) (string, error) {
	if path != "" {
		return expandHome(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultConfigDir), nil
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

const (
	pathKindFile = "file"
	pathKindDir  = "dir"
)

func detectPathKind(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return pathKindDir, true, nil
		}
		return pathKindFile, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if filepath.Ext(path) == ".toml" {
		return pathKindFile, false, nil
	}
	return pathKindDir, false, nil
}

func loadFile(path string) (Profile, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var doc map[string]any
	if _, err := toml.Decode(string(buf), &doc); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	if _, ok := doc["default_connection"]; ok {
		return Profile{}, legacyFormatError(path)
	}
	if _, ok := doc["connections"]; ok {
		return Profile{}, legacyFormatError(path)
	}
	if _, ok := doc["connection"]; !ok {
		return Profile{}, fmt.Errorf("%s must define a [connection] table; use ~/.config/dbx/<name>.toml", path)
	}
	var cfg fileConfig
	if _, err := toml.Decode(string(buf), &cfg); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Connection.normalizePaths(filepath.Dir(path)); err != nil {
		return Profile{}, err
	}
	return Profile{
		Name:       strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Path:       path,
		Connection: cfg.Connection,
	}, nil
}

func legacyFormatError(path string) error {
	return fmt.Errorf("%s uses the legacy multi-connection format; use ~/.config/dbx/<name>.toml with a [connection] table", path)
}

func (c *ConnectionConfig) normalizePaths(baseDir string) error {
	var err error
	c.Path, err = resolveResourcePath(baseDir, c.Path, true)
	if err != nil {
		return err
	}
	c.Password.File, err = resolveResourcePath(baseDir, c.Password.File, false)
	if err != nil {
		return err
	}
	return nil
}

func resolveResourcePath(baseDir, value string, allowSQLiteSpecial bool) (string, error) {
	if value == "" {
		return "", nil
	}
	if allowSQLiteSpecial && (value == ":memory:" || strings.HasPrefix(value, "sqlite:") || strings.HasPrefix(value, "file:")) {
		return value, nil
	}
	expanded, err := expandHome(value)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return expanded, nil
	}
	return filepath.Clean(filepath.Join(baseDir, expanded)), nil
}
