package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func extOf(f string) string {
	return filepath.Ext(f)
}

func readFile(f string) ([]byte, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f, err)
	}
	return b, nil
}

// printJSON marshals v as indented JSON to stdout.
func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
