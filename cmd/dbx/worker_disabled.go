//go:build !dbmod

package main

import "context"

func runModuleWorker(context.Context, []string) (bool, int) { return false, 0 }
