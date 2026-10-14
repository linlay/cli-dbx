package conn

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/linlay/cli-dbx/internal/action"
	"github.com/linlay/cli-dbx/internal/config"
)

type Spec struct {
	Name           string
	Engine         string
	Driver         string
	ODBC           *ODBCConfig
	DriverDir      string
	DriverPath     string
	DSN            string
	Connector      driver.Connector `json:"-"`
	Host           string
	Port           int
	User           string
	Database       string
	Schema         string
	Path           string
	Role           string
	AllowActions   []action.Action
	AllowTables    []string
	Tags           []string
	ReadOnly       bool
	Timeout        time.Duration
	Environment    string
	SecretSources  map[string]string
	Warnings       []string
	DisplayTarget  string
	ProductionLike bool
}

type ODBCConfig struct {
	DriverPath string
	Password   string
	Options    map[string]string
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
		DriverDir:     profile.Connection.DriverDir,
		DriverPath:    profile.Connection.DriverPath,
		Tags:          profile.Connection.Tags,
		AllowTables:   parseAllowTables(profile.Connection.AllowTables),
		ReadOnly:      profile.Connection.ReadOnly,
		Timeout:       profile.Connection.EffectiveTimeout(),
		SecretSources: map[string]string{},
	}
	selectedDriver := strings.ToLower(strings.TrimSpace(profile.Connection.Driver))
	if selectedDriver != "" && selectedDriver != "native" && selectedDriver != "odbc" {
		return Spec{}, fmt.Errorf("connection driver must be native or odbc")
	}
	if selectedDriver == "odbc" {
		if spec.Engine != "oracle" && spec.Engine != "dm" && spec.Engine != "sqlserver" {
			return Spec{}, fmt.Errorf("ODBC is supported only for oracle, dm, and sqlserver")
		}
		if profile.Connection.DSN != "" || profile.Connection.DSNEnv != "" {
			return Spec{}, fmt.Errorf("ODBC requires structured connection fields, not dsn or dsn_env")
		}
		if spec.DriverPath == "" || profile.Connection.ODBCDriver == "" {
			return Spec{}, fmt.Errorf("ODBC requires driver_path and odbc_driver")
		}
		spec.Driver = "odbc"
		spec.ODBC = &ODBCConfig{DriverPath: profile.Connection.ODBCDriver, Options: profile.Connection.ODBCOptions}
	} else if profile.Connection.ODBCDriver != "" || len(profile.Connection.ODBCOptions) > 0 {
		return Spec{}, fmt.Errorf("ODBC fields require driver = \"odbc\"")
	}

	actions, err := action.ParseList(profile.Connection.AllowActions)
	if err != nil {
		return Spec{}, fmt.Errorf("connection %q allow_actions: %w", profile.Name, err)
	}
	spec.AllowActions = actions

	if profile.Connection.DSN != "" {
		spec.DSN = profile.Connection.DSN
		if dsnHasPassword(spec.Engine, spec.DSN) {
			spec.Warnings = append(spec.Warnings, "connection.dsn contains an inline password; use structured connection fields with password = \"dbx-aes-gcm:v2:...\"")
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
			spec.Warnings = append(spec.Warnings, "dsn_env resolved to a DSN with an inline password; use structured connection fields with password = \"dbx-aes-gcm:v2:...\"")
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
		if spec.ODBC != nil {
			spec.ODBC.Password = password
			if _, err := BuildODBCDSN(spec, spec.ODBC.DriverPath); err != nil {
				return Spec{}, err
			}
			return finalize(spec)
		}
		switch spec.Engine {
		case "postgres":
			spec.DSN = buildPostgresDSN(spec, password, profile.Connection.SSLMode)
		case "mysql":
			spec.DSN = buildMySQLDSN(spec, password)
		case "sqlite":
			spec.DSN = buildSQLiteDSN(spec)
		case "oracle", "dm", "sqlserver":
			spec.DSN, err = buildExternalDSN(spec, password)
			if err != nil {
				return Spec{}, err
			}
		default:
			return Spec{}, fmt.Errorf("unsupported engine %q", profile.Connection.Engine)
		}
	}

	return finalize(spec)
}

func finalize(spec Spec) (Spec, error) {
	spec.Engine = normalizeEngine(spec.Engine)
	if spec.ODBC == nil {
		if err := validateExternalDSN(spec.Engine, spec.DSN); err != nil {
			return Spec{}, err
		}
	}
	switch spec.Engine {
	case "postgres":
		spec.Driver = "pgx"
	case "mysql":
		spec.Driver = "mysql"
	case "sqlite":
		spec.Driver = "sqlite"
	case "oracle", "dm", "sqlserver":
		if spec.ODBC != nil {
			spec.Driver = "odbc"
		} else {
			spec.Driver = spec.Engine
		}
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

func validateExternalDSN(engine, dsn string) error {
	switch engine {
	case "oracle", "sqlserver":
		u, err := url.Parse(dsn)
		if err != nil || u.Scheme != engine || u.Host == "" || (engine == "oracle" && u.User == nil) {
			return fmt.Errorf("%s requires a valid URL DSN", engine)
		}
		for key := range u.Query() {
			switch strings.ToLower(key) {
			case "password", "pwd", "user", "user id", "uid", "token", "secret":
				return fmt.Errorf("%s credentials must be in URL user information", engine)
			}
		}
	case "dm":
		if !strings.HasPrefix(strings.ToLower(dsn), "dm://") {
			return fmt.Errorf("dm requires a dm:// DSN")
		}
		end := strings.LastIndex(dsn, "?")
		if end < 0 {
			end = len(dsn)
		} else {
			for _, property := range strings.Split(dsn[end+1:], "&") {
				key, _, _ := strings.Cut(property, "=")
				if !strings.EqualFold(key, "connectTimeout") {
					return fmt.Errorf("dm DSN permits only connectTimeout in query properties")
				}
			}
		}
		at := strings.LastIndex(dsn[:end], "@")
		if at < len("dm://") || !strings.Contains(dsn[len("dm://"):at], ":") {
			return fmt.Errorf("dm requires user and password in its DSN")
		}
	}
	return nil
}

func (s Spec) AllowsAction(target action.Action) bool {
	return action.Contains(s.AllowActions, target)
}

func (s Spec) RestrictsTables() bool {
	return len(s.AllowTables) > 0
}

func (s Spec) AllowsTable(object string) bool {
	if !s.RestrictsTables() {
		return true
	}
	object = normalizeTablePattern(object)
	if object == "" {
		return false
	}
	for _, pattern := range s.AllowTables {
		if tablePatternMatches(pattern, object) {
			return true
		}
	}
	return false
}

func parseAllowTables(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, part := range raw {
		pattern := normalizeTablePattern(part)
		if pattern == "" {
			continue
		}
		key := strings.ToLower(pattern)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, pattern)
	}
	return out
}

func normalizeTablePattern(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`\"'")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ".")
	for i, part := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(part), "`\"'")
	}
	return strings.Join(parts, ".")
}

func tablePatternMatches(pattern, object string) bool {
	pattern = strings.ToLower(normalizeTablePattern(pattern))
	object = strings.ToLower(normalizeTablePattern(object))
	if pattern == "" || object == "" {
		return false
	}
	if !strings.Contains(pattern, ".") && strings.Contains(object, ".") {
		parts := strings.Split(object, ".")
		object = parts[len(parts)-1]
	}
	return wildcardMatch(pattern, object)
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	if parts[0] != "" && !strings.HasPrefix(value, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		idx := strings.Index(value[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value, last)
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
	if spec.Path == "" {
		return "file::memory:"
	}
	// Keep URI options separate so escaping a literal filename cannot change
	// mode=ro, memory/cache settings, or driver-specific query parameters.
	path, query, hasQuery := strings.Cut(spec.Path, "?")
	dsn := sqliteFileURI(path, runtime.GOOS == "windows")
	if hasQuery {
		dsn += "?" + query
	}
	return dsn
}

func sqliteFileURI(path string, windows bool) string {
	if windows {
		path = strings.ReplaceAll(path, `\`, "/")
		if len(path) >= 3 && path[1] == ':' && path[2] == '/' &&
			(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') {
			path = "/" + path
		}
	}
	prefix := "file:"
	if strings.HasPrefix(path, "//") {
		// Preserve the leading slashes of UNC paths with an empty authority.
		// file://server/... would require SQLITE_ALLOW_URI_AUTHORITY.
		prefix += "//"
	}
	return prefix + (&url.URL{Path: path}).EscapedPath()
}

func buildExternalDSN(spec Spec, password string) (string, error) {
	if spec.Host == "" {
		return "", fmt.Errorf("%s requires host", spec.Engine)
	}
	switch spec.Engine {
	case "oracle":
		if spec.Database == "" {
			return "", fmt.Errorf("oracle requires database")
		}
		u := url.URL{Scheme: "oracle", User: url.UserPassword(spec.User, password), Host: net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 1521))), Path: "/" + spec.Database}
		return u.String(), nil
	case "sqlserver":
		q := url.Values{}
		q.Set("database", spec.Database)
		u := url.URL{Scheme: "sqlserver", User: url.UserPassword(spec.User, password), Host: net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 1433))), RawQuery: q.Encode()}
		return u.String(), nil
	case "dm":
		if strings.ContainsAny(spec.User, ":\x00") || strings.ContainsRune(password, 0) {
			return "", fmt.Errorf("invalid DM credentials")
		}
		return fmt.Sprintf("dm://%s:%s@%s?connectTimeout=%d", spec.User, password, net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 5236))), spec.Timeout.Milliseconds()), nil
	}
	return "", fmt.Errorf("unsupported engine %q", spec.Engine)
}

func inferEngine(dsn string) string {
	raw := strings.ToLower(dsn)
	switch {
	case strings.HasPrefix(raw, "postgres://"), strings.HasPrefix(raw, "postgresql://"):
		return "postgres"
	case strings.HasPrefix(raw, "mysql://"):
		return "mysql"
	case strings.HasPrefix(raw, "oracle://"):
		return "oracle"
	case strings.HasPrefix(raw, "dm://"):
		return "dm"
	case strings.HasPrefix(raw, "sqlserver://"):
		return "sqlserver"
	case strings.HasPrefix(raw, "sqlite:"), strings.HasSuffix(raw, ".db"), strings.HasSuffix(raw, ".sqlite"):
		return "sqlite"
	default:
		return ""
	}
}

func dsnHasPassword(engine, dsn string) bool {
	switch normalizeEngine(engine) {
	case "postgres", "oracle", "dm", "sqlserver":
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
	case "postgres", "postgresql", "pg":
		return "postgres"
	case "oracle":
		return "oracle"
	case "dm", "dameng":
		return "dm"
	case "sqlserver", "sql_server", "mssql":
		return "sqlserver"
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
