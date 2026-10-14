package conn

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// BuildODBCDSN constructs a connection string in memory for the child process.
// driverName is an absolute vendor library on Unix or its registered name on Windows.
func BuildODBCDSN(spec Spec, driverName string) (string, error) {
	if spec.ODBC == nil || spec.Driver != "odbc" {
		return "", fmt.Errorf("ODBC connection configuration is required")
	}
	if driverName == "" {
		return "", fmt.Errorf("ODBC driver name is required")
	}
	if spec.Host == "" {
		return "", fmt.Errorf("%s requires host", spec.Engine)
	}
	attrs := map[string]string{"DRIVER": driverName, "UID": spec.User, "PWD": spec.ODBC.Password}
	switch spec.Engine {
	case "oracle":
		if spec.Database == "" {
			return "", fmt.Errorf("oracle requires database")
		}
		attrs["DBQ"] = "//" + net.JoinHostPort(spec.Host, strconv.Itoa(defaultPort(spec.Port, 1521))) + "/" + spec.Database
	case "dm":
		attrs["SERVER"] = spec.Host
		attrs["TCP_PORT"] = strconv.Itoa(defaultPort(spec.Port, 5236))
	case "sqlserver":
		host := spec.Host
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			host = "[" + host + "]"
		}
		attrs["SERVER"] = "tcp:" + host + "," + strconv.Itoa(defaultPort(spec.Port, 1433))
		if spec.Database != "" {
			attrs["DATABASE"] = spec.Database
		}
	default:
		return "", fmt.Errorf("ODBC is supported only for oracle, dm, and sqlserver")
	}
	protected := map[string]bool{}
	for _, key := range []string{"DRIVER", "DSN", "FILEDSN", "SAVEFILE", "UID", "USER", "USER ID", "USERID", "PWD", "PASSWORD", "DBQ", "SERVER", "ADDRESS", "ADDR", "NETWORK ADDRESS", "TCP_PORT", "PORT", "DATABASE"} {
		protected[key] = true
	}
	seen := map[string]bool{}
	for raw, value := range spec.ODBC.Options {
		key := strings.ToUpper(strings.TrimSpace(raw))
		if key == "" {
			return "", fmt.Errorf("empty odbc_options key")
		}
		for _, r := range key {
			if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ' ') {
				return "", fmt.Errorf("invalid odbc_options key")
			}
		}
		if seen[key] {
			return "", fmt.Errorf("duplicate odbc_options key")
		}
		seen[key] = true
		if protected[key] {
			return "", fmt.Errorf("odbc_options cannot override driver, credentials, or structured connection fields")
		}
		if key == "RETRYEXEC" {
			return "", fmt.Errorf("odbc_options cannot enable automatic statement retries")
		}
		attrs[key] = value
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		value := attrs[key]
		if strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("ODBC connection fields cannot contain NUL")
		}
		fmt.Fprintf(&out, "%s={%s};", key, strings.ReplaceAll(value, "}", "}}"))
	}
	return out.String(), nil
}
