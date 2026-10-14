module github.com/linlay/cli-dbx/drivers/odbc

go 1.26.6

require github.com/linlay/cli-dbx v0.0.0

require github.com/pkujhd/goloader v0.0.0 // indirect

require (
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/alexbrainman/odbc v0.0.0-20250601004241-49e6b2bc0cf0
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/sys v0.28.0
)

replace github.com/linlay/cli-dbx => ../..

replace github.com/spf13/cobra => ../../third_party/cobra

replace github.com/alexbrainman/odbc => ./fork

replace github.com/pkujhd/goloader => ../../third_party/goloader
