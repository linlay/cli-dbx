package conn

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/linlay/cli-dbx/internal/config"
	"github.com/linlay/cli-dbx/internal/odbcdriver"
)

func buildODBCDSN(ctx context.Context, c config.ConnectionConfig, engine, password string) (string, error) {
	found, err := odbcdriver.FindFile(ctx, c.DriverDir, engine, c.ODBCDriver)
	if err != nil {
		return "", err
	}
	name, err := odbcdriver.ConnectionName(found.Path)
	if err != nil {
		return "", err
	}
	return odbcConnectionString(c, engine, password, name)
}

func odbcConnectionString(c config.ConnectionConfig, engine, password, driverName string) (string, error) {
	if c.Host == "" && engine != "sqlite" {
		return "", fmt.Errorf("%s requires host", engine)
	}
	attrs := map[string]string{"DRIVER": driverName, "UID": c.User, "PWD": password}
	port := c.Port
	switch engine {
	case "oracle":
		if c.Database == "" {
			return "", fmt.Errorf("oracle requires database")
		}
		attrs["DBQ"] = "//" + net.JoinHostPort(c.Host, strconv.Itoa(defaultPort(port, 1521))) + "/" + c.Database
	case "dm":
		attrs["SERVER"] = c.Host
		attrs["TCP_PORT"] = strconv.Itoa(defaultPort(port, 5236))
	case "sqlserver":
		host := c.Host
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		attrs["SERVER"] = "tcp:" + host + "," + strconv.Itoa(defaultPort(port, 1433))
		if c.Database != "" {
			attrs["DATABASE"] = c.Database
		}
	case "mysql", "postgres":
		fallback := 3306
		if engine == "postgres" {
			fallback = 5432
		}
		attrs["SERVER"] = c.Host
		attrs["PORT"] = strconv.Itoa(defaultPort(port, fallback))
		if c.Database != "" {
			attrs["DATABASE"] = c.Database
		}
	case "sqlite":
		attrs["DATABASE"] = c.Path
	default:
		// Other vendors have different attribute names. Use explicit
		// odbc_options instead of guessing a connection string dialect.
	}
	for key, value := range c.ODBCOptions {
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" {
			return "", fmt.Errorf("empty odbc_options key")
		}
		for _, r := range key {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != ' ' {
				return "", fmt.Errorf("invalid odbc_options key")
			}
		}
		switch key {
		case "DRIVER", "DSN", "FILEDSN", "SAVEFILE", "UID", "USER", "USER ID", "USERID", "PWD", "PASSWORD":
			return "", fmt.Errorf("odbc_options cannot override driver or credentials")
		}
		if _, ok := attrs[key]; ok {
			return "", fmt.Errorf("odbc_options cannot override structured connection field %s", key)
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
