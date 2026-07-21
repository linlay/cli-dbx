package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/secret"
)

const (
	defaultConfigDir   = ".config/dbx"
	agentConfigHomeEnv = "AP_AGENT_CONFIG_HOME"
)

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
	Tags         []string    `toml:"tags"`
	AllowActions []string    `toml:"allow_actions"`
	AllowTables  []string    `toml:"allow_tables"`
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
	resolvedPaths, displayPath, err := resolvePaths(path)
	if err != nil {
		return nil, "", err
	}
	if path != "" {
		profiles, err := listProfiles(resolvedPaths[0])
		return profiles, displayPath, err
	}
	// Load the system directories first so a same-named agent connection wins.
	byName := map[string]Profile{}
	for index := len(resolvedPaths) - 1; index >= 0; index-- {
		profiles, err := listProfiles(resolvedPaths[index])
		if err != nil {
			return nil, displayPath, err
		}
		for _, profile := range profiles {
			byName[profile.Name] = profile
		}
	}
	profiles := make([]Profile, 0, len(byName))
	for _, profile := range byName {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, displayPath, nil
}

func LoadNamed(path, name string) (Profile, error) {
	if strings.TrimSpace(name) == "" {
		return Profile{}, fmt.Errorf("no connection selected")
	}
	resolvedPaths, _, err := resolvePaths(path)
	if err != nil {
		return Profile{}, err
	}
	for _, resolvedPath := range resolvedPaths {
		kind, exists, err := detectPathKind(resolvedPath)
		if err != nil {
			return Profile{}, err
		}
		if kind == pathKindFile {
			if !exists {
				continue
			}
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
		if _, err := os.Stat(configFile); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Profile{}, err
		}
		// A present primary config is authoritative: parsing errors must not
		// silently fall through to a system connection with the same name.
		return loadFile(configFile)
	}
	return Profile{}, fmt.Errorf("connection %q not found; expected %s", name, filepath.Join(resolvedPaths[0], name+".toml"))
}

func resolvePaths(path string) ([]string, string, error) {
	if path != "" {
		resolvedPath, err := expandHome(path)
		if err != nil {
			return nil, "", err
		}
		return []string{resolvedPath}, resolvedPath, nil
	}
	paths, err := defaultConfigPaths()
	if err != nil {
		return nil, "", err
	}
	return paths, paths[0], nil
}

func defaultConfigPaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	systemConfigDir := filepath.Join(home, defaultConfigDir)
	if agentConfigHome := strings.TrimSpace(os.Getenv(agentConfigHomeEnv)); agentConfigHome != "" {
		return appendUniquePath([]string{filepath.Join(agentConfigHome, "dbx")}, systemConfigDir), nil
	}
	return []string{systemConfigDir}, nil
}

func appendUniquePath(paths []string, path string) []string {
	for _, existing := range paths {
		if filepath.Clean(existing) == filepath.Clean(path) {
			return paths
		}
	}
	return append(paths, path)
}

func listProfiles(resolvedPath string) ([]Profile, error) {
	kind, exists, err := detectPathKind(resolvedPath)
	if err != nil {
		return nil, err
	}
	if kind == pathKindFile {
		if !exists {
			return []Profile{}, nil
		}
		profile, err := loadFile(resolvedPath)
		if err != nil {
			return nil, err
		}
		return []Profile{profile}, nil
	}
	if !exists {
		return []Profile{}, nil
	}
	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		profile, err := loadFile(filepath.Join(resolvedPath, entry.Name()))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
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
	value, source, _, err := v.ResolveWithWarnings(ctx)
	return value, source, err
}

func (v ValueSource) ResolveWithWarnings(ctx context.Context) (string, string, []string, error) {
	if v.Value != "" {
		if secret.IsEncryptedValue(v.Value) {
			value, err := secret.DecryptString(ctx, v.Value)
			if err != nil {
				return "", "encrypted", nil, err
			}
			return value, "encrypted", nil, nil
		}
		return v.Value, "plaintext", []string{"plaintext password is not recommended; use dbx secret encrypt and store password = \"dbx-aes-gcm:v2:...\""}, nil
	}
	if v.Env != "" {
		return "", "env", nil, fmt.Errorf("password.env is disabled; store an encrypted password in password = \"dbx-aes-gcm:v2:...\"")
	}
	if v.File != "" {
		return "", "file", nil, fmt.Errorf("password.file is disabled; store an encrypted password in password = \"dbx-aes-gcm:v2:...\"")
	}
	if len(v.Cmd) > 0 {
		return "", "cmd", nil, fmt.Errorf("password.cmd is disabled; store an encrypted password in password = \"dbx-aes-gcm:v2:...\"")
	}
	return "", "", nil, nil
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
	connDoc, ok := doc["connection"].(map[string]any)
	if !ok {
		return Profile{}, fmt.Errorf("%s must define a [connection] table; use ~/.config/dbx/<name>.toml", path)
	}
	if _, ok := connDoc["mode"]; ok {
		return Profile{}, fmt.Errorf("%s: mode has been removed; use allow_actions = [...]", path)
	}
	var cfg fileConfig
	if _, err := toml.Decode(string(buf), &cfg); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Connection.normalizePaths(filepath.Dir(path)); err != nil {
		return Profile{}, err
	}
	if len(cfg.Connection.AllowActions) == 0 {
		return Profile{}, fmt.Errorf("%s: connection.allow_actions is required", path)
	}
	actions, err := action.ParseList(cfg.Connection.AllowActions)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: connection allow_actions: %w", path, err)
	}
	cfg.Connection.AllowActions = action.Strings(actions)
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
