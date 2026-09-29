package conn

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// buildNativeDSN handles all built-in Go drivers. ODBC uses buildODBCDSN.
func buildNativeDSN(spec Spec, password, sslmode string) (string, error) {
	// Preserve the original MySQL/PostgreSQL/SQLite host and path defaults.
	if spec.Host == "" && (spec.Engine == "oracle" || spec.Engine == "dm" || spec.Engine == "sqlserver") {
		return "", fmt.Errorf("%s requires host", spec.Engine)
	}
	switch spec.Engine {
	case "postgres":
		return buildPostgresDSN(spec, password, sslmode), nil
	case "mysql":
		return buildMySQLDSN(spec, password), nil
	case "sqlite":
		return buildSQLiteDSN(spec), nil
	case "oracle":
		if spec.Database == "" {
			return "", fmt.Errorf("oracle requires database")
		}
		// DBX keeps credentials in a URL; the execution layer passes them
		// separately to the official Oracle connector (not its DSN parser).
		u := url.URL{Scheme: "oracle", User: url.UserPassword(spec.User, password),
			Host: net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 1521))), Path: "/" + spec.Database}
		return u.String(), nil
	case "sqlserver":
		q := url.Values{}
		q.Set("database", spec.Database)
		u := url.URL{Scheme: "sqlserver", User: url.UserPassword(spec.User, password),
			Host: net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 1433))), RawQuery: q.Encode()}
		return u.String(), nil
	case "dm":
		// This driver's DSN parser reads credentials literally, and uses the
		// last '?' and '@' as delimiters. Always append a query component.
		if strings.ContainsAny(spec.User, ":\x00") || strings.ContainsRune(password, 0) {
			return "", fmt.Errorf("invalid DM credentials")
		}
		return fmt.Sprintf("dm://%s:%s@%s?connectTimeout=%d", spec.User, password,
			net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 5236))), spec.Timeout.Milliseconds()), nil
	}
	return "", fmt.Errorf("unsupported native engine %q", spec.Engine)
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
