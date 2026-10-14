//go:build !windows

package driverprocess

func ensureWorkerLifetime() error { return nil }
