package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MaLuns/wallhaven-electron/internal/changelog"
)

func main() {
	version := flag.String("version", "", "release version to read from log.json")
	output := flag.String("o", "", "Markdown output file; stdout when empty")
	flag.Parse()
	notes, err := changelog.Markdown(*version)
	if err == nil && *output != "" {
		err = os.MkdirAll(filepath.Dir(*output), 0o755)
		if err == nil {
			err = os.WriteFile(*output, []byte(notes), 0o644)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
	if *output == "" {
		fmt.Print(notes)
	}
}
