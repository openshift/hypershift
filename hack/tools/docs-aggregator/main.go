package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	docsContentDir = "docs/content"
	outputFile     = "docs/content/reference/aggregated-docs.md"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Find all markdown files in the docs/content directory
	mdFiles, err := findMarkdownFiles(docsContentDir)
	if err != nil {
		return fmt.Errorf("finding markdown files: %w", err)
	}

	// Sort files for consistent output
	sort.Strings(mdFiles)

	// Build the aggregated content
	var builder strings.Builder

	// Write header
	builder.WriteString("# HyperShift Documentation (Aggregated)\n\n")
	builder.WriteString("This file contains all HyperShift documentation aggregated into a single file\n")
	builder.WriteString("for use with AI tools like NotebookLM.\n\n")
	builder.WriteString(fmt.Sprintf("Total documents: %d\n\n", len(mdFiles)))

	// Process each file
	for _, filePath := range mdFiles {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading file %s: %w", filePath, err)
		}

		// Write separator and source header
		builder.WriteString("---\n\n")
		builder.WriteString(fmt.Sprintf("## Source: %s\n\n", filePath))
		builder.WriteString(string(content))
		builder.WriteString("\n\n")
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(outputFile)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Write the aggregated file
	if err := os.WriteFile(outputFile, []byte(builder.String()), 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	fmt.Printf("Successfully aggregated %d documentation files to %s\n", len(mdFiles), outputFile)
	return nil
}

// findMarkdownFiles walks the directory tree and returns all .md files,
// excluding the output file itself to avoid including it in aggregation.
func findMarkdownFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the output file to avoid including it in aggregation
		if path == outputFile {
			return nil
		}

		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}
