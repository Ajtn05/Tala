package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func setupLogging(output string) {
	if e := os.MkdirAll(output, 0700); e != nil {
		log.Printf("create log directory: %v", e)
		return
	}
	f, e := os.OpenFile(filepath.Join(output, "studio.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
}
