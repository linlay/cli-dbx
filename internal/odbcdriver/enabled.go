//go:build (windows && (amd64 || 386)) || (odbc && cgo && (darwin || linux))

package odbcdriver

import _ "github.com/alexbrainman/odbc"
