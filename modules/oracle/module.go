//go:build dbmod

package oracle

import (
	"fmt"
	"github.com/linlay/cli-dbx/internal/conn"
	"github.com/oracle/go-oracledb/v26/oracle"
	"net/url"
)

func Initialize() error { return nil }

func Prepare(spec *conn.Spec) error {
	u, err := url.Parse(spec.DSN)
	if err != nil || u.Scheme != "oracle" || u.User == nil || u.Host == "" || u.Path == "" {
		return fmt.Errorf("invalid Oracle connection; use structured connection fields")
	}
	cfg := oracle.NewOracleDriverConfig()
	cfg.ConnectDescriptor = "tcp://" + u.Host + u.EscapedPath()
	cfg.Credentials.User = u.User.Username()
	cfg.Credentials.Password, _ = u.User.Password()
	connector, err := oracle.NewOracleConnector(cfg)
	if err != nil {
		return fmt.Errorf("invalid Oracle connector configuration")
	}
	spec.Connector = connector
	return nil
}
