package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
)

//go:embed text.txt
var filetext string

func main() {
	filepath := "helper_tools/text.txt"
	bts := []byte(filetext)
	bts = bytes.ReplaceAll(bts, []byte("\r\n"), []byte(`\n`))
	bts = bytes.ReplaceAll(bts, []byte("\n"), []byte(`\n`))
	if err := os.WriteFile(filepath, bts, 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

}
