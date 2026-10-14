//go:build !windows

package main

func connectionName(path string) (string, error) { return path, nil }
