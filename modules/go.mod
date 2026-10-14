module github.com/linlay/cli-dbx/modules

go 1.26.6

require (
	gitee.com/chunanyong/dm v1.8.22
	github.com/jackc/pgx/v5 v5.7.2
	github.com/linlay/cli-dbx v0.0.0
	github.com/microsoft/go-mssqldb v1.8.0
	github.com/oracle/go-oracledb/v26 v26.0.1-beta
)

require (
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/snappy v0.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

replace github.com/linlay/cli-dbx => ..

replace github.com/spf13/cobra => ../third_party/cobra

replace github.com/pkujhd/goloader => ../third_party/goloader

replace gitee.com/chunanyong/dm => ../drivers/dm/fork
