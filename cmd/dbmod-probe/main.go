//go:build dbmod

package main

import (
	"fmt"
	"github.com/linlay/cli-dbx/internal/dbmodule"
	"os"
	"runtime"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	loaded, err := dbmodule.Load(f, os.Args[2], 0)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtime.GC()
	runtime.KeepAlive(loaded)
	fmt.Println("module loaded and GC passed")
}
