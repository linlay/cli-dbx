module github.com/linlay/cli-dbx/drivers/sqlserver

go 1.26.6

require (
	github.com/linlay/cli-dbx v0.0.0
	github.com/microsoft/go-mssqldb v1.8.0
)

require (
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pkujhd/goloader v0.0.0 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)

replace github.com/linlay/cli-dbx => ../..

replace github.com/spf13/cobra => ../../third_party/cobra

replace github.com/pkujhd/goloader => ../../third_party/goloader
