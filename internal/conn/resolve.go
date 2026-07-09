package conn

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/config"
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
	AllowActions   []action.Action
	Tags           []string
	ReadOnly       bool
	Timeout        time.Duration
	Environment    string
	SecretSources  map[string]string
	Warnings       []string
	DisplayTarget  string
	ProductionLike bool
}

type ResolveInput struct {
	ConfigPath string
	Name       string
	DSN        string
	Engine     string
	Action     action.Action
}

func Resolve(ctx context.Context, in ResolveInput) (Spec, error) {
	if in.DSN != "" {
		engine := in.Engine
		if engine == "" {
			engine = inferEngine(in.DSN)
		}
		spec := Spec{
			Name:         "adhoc",
			Engine:       engine,
			DSN:          in.DSN,
			AllowActions: []action.Action{in.Action},
			Timeout:      15 * time.Second,
		}
		return finalize(spec)
	}

	if strings.TrimSpace(in.Name) == "" {
		return Spec{}, fmt.Errorf("no connection selected")
	}

	profile, err := config.LoadNamed(in.ConfigPath, in.Name)
	if err != nil {
		return Spec{}, err
	}

	spec := Spec{
		Name:          profile.Name,
		Engine:        normalizeEngine(profile.Connection.Engine),
		Host:          profile.Connection.Host,
		Port:          profile.Connection.Port,
		User:          profile.Connection.User,
		Database:      profile.Connection.Database,
		Schema:        profile.Connection.Schema,
		Path:          profile.Connection.Path,
		Role:          profile.Connection.Role,
		Tags:          profile.Connection.Tags,
		ReadOnly:      profile.Connection.ReadOnly,
		Timeout:       profile.Connection.EffectiveTimeout(),
		SecretSources: map[string]string{},
	}

	actions, err := action.ParseList(profile.Connection.AllowActions)
	if err != nil {
		return Spec{}, fmt.Errorf("connection %q allow_actions: %w", profile.Name, err)
	}
	spec.AllowActions = actions

	if profile.Connection.DSN != "" {
		spec.DSN = profile.Connection.DSN
		if dsnHasPassword(spec.Engine, spec.DSN) {
			spec.Warnings = append(spec.Warnings, "connection.dsn contains an inline password; use structured connection fields with password = \"dbx-aes-gcm:v1:...\"")
		}
		if spec.Engine == "" {
			spec.Engine = inferEngine(profile.Connection.DSN)
		}
	}
	if profile.Connection.DSNEnv != "" {
		value, ok := os.LookupEnv(profile.Connection.DSNEnv)
		if !ok {
			return Spec{}, fmt.Errorf("connection %q dsn_env %q is not set", profile.Name, profile.Connection.DSNEnv)
		}
		spec.DSN = value
		spec.SecretSources["dsn"] = "env"
		if dsnHasPassword(spec.Engine, value) {
			spec.Warnings = append(spec.Warnings, "dsn_env resolved to a DSN with an inline password; use structured connection fields with password = \"dbx-aes-gcm:v1:...\"")
		}
		if spec.Engine == "" {
			spec.Engine = inferEngine(value)
		}
	}

	if spec.DSN == "" {
		password, source, warnings, err := profile.Connection.Password.ResolveWithWarnings(ctx)
		if err != nil {
			return Spec{}, fmt.Errorf("connection %q password: %w", profile.Name, err)
		}
		spec.Warnings = append(spec.Warnings, warnings...)
		if source != "" {
			spec.SecretSources["password"] = source
		}
		switch spec.Engine {
		case "postgres":
			spec.DSN = buildPostgresDSN(spec, password, profile.Connection.SSLMode)
		case "mysql":
			spec.DSN = buildMySQLDSN(spec, password)
		case "sqlite":
			spec.DSN = buildSQLiteDSN(spec)
		default:
			return Spec{}, fmt.Errorf("unsupported engine %q", profile.Connection.Engine)
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
	if len(spec.AllowActions) == 0 {
		return Spec{}, fmt.Errorf("connection %q must define allow_actions", spec.Name)
	}
	spec.Environment = inferEnvironment(spec.Name, spec.Tags)
	spec.ProductionLike = spec.Environment == "prod"
	if spec.ProductionLike && (action.Contains(spec.AllowActions, action.Schema) || action.Contains(spec.AllowActions, action.Admin)) {
		return Spec{}, fmt.Errorf("connection %q is tagged as prod-like and blocks actions %v by default", spec.Name, action.Strings(spec.AllowActions))
	}
	spec.DisplayTarget = displayTarget(spec)
	return spec, nil
}

func (s Spec) AllowsAction(target action.Action) bool {
	return action.Contains(s.AllowActions, target)
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
	if strings.HasPrefix(spec.Path, "sqlite:") || strings.HasPrefix(spec.Path, "file:") {
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

func dsnHasPassword(engine, dsn string) bool {
	switch normalizeEngine(engine) {
	case "postgres":
		if u, err := url.Parse(dsn); err == nil && u.User != nil {
			_, ok := u.User.Password()
			return ok
		}
	case "mysql":
		if idx := strings.Index(dsn, "@"); idx > 0 {
			return strings.Contains(dsn[:idx], ":")
		}
	default:
		raw := strings.ToLower(dsn)
		if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
			if u, err := url.Parse(dsn); err == nil && u.User != nil {
				_, ok := u.User.Password()
				return ok
			}
		}
		if idx := strings.Index(dsn, "@"); idx > 0 {
			return strings.Contains(dsn[:idx], ":")
		}
	}
	return false
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
