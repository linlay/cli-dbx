package conn

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linlay/dbx/internal/config"
	"github.com/linlay/dbx/internal/mode"
)

type Spec struct {
	Name           string
	Engine         string
	Driver         string
	DSN            string
	Host           string
	Port           int
	User           string
	Database       string
	Schema         string
	Path           string
	Role           string
	Mode           mode.Mode
	Tags           []string
	ReadOnly       bool
	Timeout        time.Duration
	Environment    string
	SecretSources  map[string]string
	DisplayTarget  string
	ProductionLike bool
}

type ResolveInput struct {
	ConfigPath string
	Config     *config.Config
	Name       string
	DSN        string
	Engine     string
	Mode       string
}

func Resolve(ctx context.Context, in ResolveInput) (Spec, error) {
	cfg := in.Config
	if cfg == nil {
		var err error
		cfg, _, err = config.Load(in.ConfigPath)
		if err != nil {
			return Spec{}, err
		}
	}

	if in.DSN != "" {
		engine := in.Engine
		if engine == "" {
			engine = inferEngine(in.DSN)
		}
		spec := Spec{
			Name:    "adhoc",
			Engine:  engine,
			DSN:     in.DSN,
			Mode:    mode.MustParse(in.Mode),
			Timeout: 15 * time.Second,
		}
		return finalize(spec)
	}

	if envDSN := strings.TrimSpace(os.Getenv("DBX_DSN")); envDSN != "" && in.Name == "" {
		engine := in.Engine
		if engine == "" {
			engine = strings.TrimSpace(os.Getenv("DBX_ENGINE"))
		}
		if engine == "" {
			engine = inferEngine(envDSN)
		}
		spec := Spec{
			Name:    "env",
			Engine:  engine,
			DSN:     envDSN,
			Mode:    mode.MustParse(in.Mode),
			Timeout: 15 * time.Second,
		}
		return finalize(spec)
	}

	name := in.Name
	if name == "" {
		if envName := strings.TrimSpace(os.Getenv("DBX_CONN")); envName != "" {
			name = envName
		}
	}
	if name == "" {
		name = cfg.DefaultConnection
	}
	if name == "" {
		return Spec{}, fmt.Errorf("no connection selected")
	}

	profile, ok := cfg.Connections[name]
	if !ok {
		return Spec{}, fmt.Errorf("connection %q not found", name)
	}

	spec := Spec{
		Name:          name,
		Engine:        normalizeEngine(profile.Engine),
		Host:          profile.Host,
		Port:          profile.Port,
		User:          profile.User,
		Database:      profile.Database,
		Schema:        profile.Schema,
		Path:          profile.Path,
		Role:          profile.Role,
		Tags:          profile.Tags,
		ReadOnly:      profile.ReadOnly,
		Timeout:       profile.EffectiveTimeout(),
		SecretSources: map[string]string{},
	}

	selectedMode := profile.Mode
	if in.Mode != "" {
		selectedMode = in.Mode
	}
	spec.Mode = mode.MustParse(selectedMode)

	if profile.DSN != "" {
		spec.DSN = profile.DSN
		if spec.Engine == "" {
			spec.Engine = inferEngine(profile.DSN)
		}
	}
	if profile.DSNEnv != "" {
		value, ok := os.LookupEnv(profile.DSNEnv)
		if !ok {
			return Spec{}, fmt.Errorf("connection %q dsn_env %q is not set", name, profile.DSNEnv)
		}
		spec.DSN = value
		spec.SecretSources["dsn"] = "env"
		if spec.Engine == "" {
			spec.Engine = inferEngine(value)
		}
	}

	if spec.DSN == "" {
		password, source, err := profile.Password.Resolve(ctx)
		if err != nil {
			return Spec{}, fmt.Errorf("connection %q password: %w", name, err)
		}
		if source != "" {
			spec.SecretSources["password"] = source
		}
		switch spec.Engine {
		case "postgres":
			spec.DSN = buildPostgresDSN(spec, password, profile.SSLMode)
		case "mysql":
			spec.DSN = buildMySQLDSN(spec, password)
		case "sqlite":
			spec.DSN = buildSQLiteDSN(spec)
		default:
			return Spec{}, fmt.Errorf("unsupported engine %q", profile.Engine)
		}
	}

	return finalize(spec)
}

func finalize(spec Spec) (Spec, error) {
	spec.Engine = normalizeEngine(spec.Engine)
	switch spec.Engine {
	case "postgres":
		spec.Driver = "pgx"
	case "mysql":
		spec.Driver = "mysql"
	case "sqlite":
		spec.Driver = "sqlite"
	default:
		return Spec{}, fmt.Errorf("unsupported engine %q", spec.Engine)
	}
	if spec.Mode == "" {
		spec.Mode = mode.Lantern
	}
	spec.Environment = inferEnvironment(spec.Name, spec.Tags)
	spec.ProductionLike = spec.Environment == "prod"
	if spec.ProductionLike && spec.Mode != mode.Lantern && spec.Mode != mode.Tweezers {
		return Spec{}, fmt.Errorf("connection %q is tagged as prod-like and blocks mode %s by default", spec.Name, spec.Mode)
	}
	spec.DisplayTarget = displayTarget(spec)
	return spec, nil
}

func inferEnvironment(name string, tags []string) string {
	candidates := append([]string{name}, tags...)
	for _, item := range candidates {
		s := strings.ToLower(item)
		switch {
		case strings.Contains(s, "prod"):
			return "prod"
		case strings.Contains(s, "stage"):
			return "staging"
		case strings.Contains(s, "dev"), strings.Contains(s, "local"):
			return "dev"
		}
	}
	return "unknown"
}

func displayTarget(spec Spec) string {
	if spec.Engine == "sqlite" {
		if spec.Path == "" {
			return spec.DSN
		}
		return spec.Path
	}
	host := spec.Host
	if host == "" && spec.DSN != "" {
		if u, err := url.Parse(spec.DSN); err == nil {
			host = u.Host
		}
	}
	if spec.Database != "" {
		return fmt.Sprintf("%s/%s", host, spec.Database)
	}
	return host
}

func buildPostgresDSN(spec Spec, password, sslmode string) string {
	query := url.Values{}
	if sslmode == "" {
		sslmode = "prefer"
	}
	query.Set("sslmode", sslmode)
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(spec.User, password),
		Host:     fmt.Sprintf("%s:%d", spec.Host, defaultPort(spec.Port, 5432)),
		Path:     spec.Database,
		RawQuery: query.Encode(),
	}
	return u.String()
}

func buildMySQLDSN(spec Spec, password string) string {
	address := fmt.Sprintf("tcp(%s:%d)", spec.Host, defaultPort(spec.Port, 3306))
	return fmt.Sprintf("%s:%s@%s/%s?parseTime=true", spec.User, password, address, spec.Database)
}

func buildSQLiteDSN(spec Spec) string {
	if strings.HasPrefix(spec.Path, "sqlite:") {
		return spec.Path
	}
	if spec.Path == "" || spec.Path == ":memory:" {
		return "file::memory:"
	}
	if filepath.IsAbs(spec.Path) {
		return "file:" + spec.Path
	}
	return "file:" + spec.Path
}

func inferEngine(dsn string) string {
	raw := strings.ToLower(dsn)
	switch {
	case strings.HasPrefix(raw, "postgres://"), strings.HasPrefix(raw, "postgresql://"):
		return "postgres"
	case strings.HasPrefix(raw, "mysql://"):
		return "mysql"
	case strings.HasPrefix(raw, "sqlite:"), strings.HasSuffix(raw, ".db"), strings.HasSuffix(raw, ".sqlite"):
		return "sqlite"
	default:
		return ""
	}
}

func normalizeEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres", "postgresql":
		return "postgres"
	case "mysql":
		return "mysql"
	case "sqlite", "sqlite3":
		return "sqlite"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

func defaultPort(port, fallback int) int {
	if port > 0 {
		return port
	}
	return fallback
}
