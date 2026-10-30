//go:build !dbmod

package driverprocess

import (
	"context"
	"github.com/linlay/cli-dbx/internal/conn"
	"sync/atomic"
)

func callModule(context.Context, conn.Spec, Request, *atomic.Bool) (Response, error) {
	return Response{}, &Error{Code: "driver_invalid", Message: "this build does not support Go object modules"}
}
