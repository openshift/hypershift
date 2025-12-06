package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"

	"github.com/alecthomas/kingpin/v2"
	"github.com/golangci/golines/internal/diff"
	"github.com/golangci/golines/internal/formatter"
	"github.com/golangci/golines/shorten"
)

// these values are provided automatically by Goreleaser.
// ref: https://goreleaser.com/customization/builds/
var (
	version = "unknown"
	commit  = "?"
	date    = ""
)

var (
	// Flags.
	baseFormatterCmd = kingpin.Flag(
		"base-formatter",
		"Base formatter to use").Default("").String()
	chainSplitDots = kingpin.Flag(
		"chain-split-dots",
		"Split chained methods on the dots as opposed to the arguments").
		Default("true").Bool()
	debugFlag = kingpin.Flag(
		"debug",
		"Show debug logs").Short('d').Default("false").Bool()
	dotFile = kingpin.Flag(
		"dot-file",
		"Path to dot representation of the AST graph (for debugging)").Default("").String()
	dryRun = kingpin.Flag(
		"dry-run",
		"Display diffs instead of rewriting files").Default("false").Bool()
	ignoreGenerated = kingpin.Flag(
		"ignore-generated",
		"Ignore generated go files").Default("true").Bool()
	ignoredDirs = kingpin.Flag(
		"ignored-dirs",
		"Directories to ignore").Default("vendor", "testdata", "node_modules").Strings()
	keepAnnotations = kingpin.Flag(
		"keep-annotations",
		"Keep shortening annotations in the final output").Default("false").Bool()
	listFiles = kingpin.Flag(
		"list-files",
		"List files that would be reformatted by this tool").Short('l').Default("false").Bool()
	maxLen = kingpin.Flag(
		"max-len",
		"Target maximum line length").Short('m').Default("100").Int()
	profile = kingpin.Flag(
		"profile",
		"Path to profile output").Default("").String()
	reformatTags = kingpin.Flag(
		"reformat-tags",
		"Reformat struct tags").Default("true").Bool()
	shortenComments = kingpin.Flag(
		"shorten-comments",
		"Shorten single-line comments").Default("false").Bool()
	tabLen = kingpin.Flag(
		"tab-len",
		"Length of a tab").Short('t').Default("4").Int()
	versionFlag = kingpin.Flag(
		"version",
		"Print out version and exit").Default("false").Bool()
	writeOutput = kingpin.Flag(
		"write-output",
		"Write result to (source) file instead of stdout").Short('w').Default("false").Bool()

	// Args.
	paths = kingpin.Arg(
		"paths",
		"Paths to format",
	).Strings()
)

func main() {
	kingpin.Parse()

	if deref(debugFlag) {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	// Arbitrarily limit in-flight work to 2MiB times the number of threads.
	//
	// The actual overhead for the parse tree and output will depend on the
	// specifics of the file, but this at least keeps the footprint of the process
	// roughly proportional to GOMAXPROCS.
	maxWeight := (2 << 20) * int64(runtime.GOMAXPROCS(0))

	s := newSequencer(maxWeight, os.Stdout, os.Stderr)

	run(s)

	os.Exit(s.GetExitCode())
}

func run(s *sequencer) {
	if deref(versionFlag) {
		printVersion(os.Stdout)

		return
	}

	if deref(profile) != "" {
		fdSem <- true

		f, err := os.Create(*profile)
		if err != nil {
			s.AddReport(fmt.Errorf("creating cpu profile: %w", err))
		}

		defer func() {
			_ = f.Close()

			<-fdSem
		}()

		_ = pprof.StartCPUProfile(f)

		defer pprof.StopCPUProfile()
	}

	NewRunner().run(s)
}

type Runner struct {
	args            []string
	ignoredDirs     []string
	ignoreGenerated bool
	dryRun          bool
	listFiles       bool
	writeOutput     bool

	shortener *shorten.Shortener

	extraFormatter *formatter.Executable
}

func NewRunner() *Runner {
	config := &shorten.Config{
		MaxLen:          deref(maxLen),
		TabLen:          deref(tabLen),
		KeepAnnotations: deref(keepAnnotations),
		ShortenComments: deref(shortenComments),
		ReformatTags:    deref(reformatTags),
		DotFile:         deref(dotFile),
		ChainSplitDots:  deref(chainSplitDots),
	}

	return &Runner{
		args:            deref(paths),
		ignoredDirs:     deref(ignoredDirs),
		ignoreGenerated: deref(ignoreGenerated),
		dryRun:          deref(dryRun),
		listFiles:       deref(listFiles),
		writeOutput:     deref(writeOutput),

		shortener:      shorten.NewShortener(config, shorten.WithLogger(slog.Default())),
		extraFormatter: formatter.NewExecutable(deref(baseFormatterCmd)),
	}
}

func (r *Runner) run(s *sequencer) {
	// Read input from stdin
	if len(r.args) == 0 {
		s.Add(0, func(rp *reporter) error {
			return r.processFile("<standard input>", nil, os.Stdin, rp)
		})

		return
	}

	// Read inputs from paths provided in arguments
	for _, arg := range r.args {
		switch info, err := os.Stat(arg); {
		case err != nil:
			s.AddReport(err)

		case !info.IsDir():
			if r.isIgnoredFile(arg) {
				return
			}

			s.Add(fileWeight(arg, info), func(rp *reporter) error {
				return r.processFile(arg, info, nil, rp)
			})

		default:
			// Path is a directory, walk it
			err = filepath.WalkDir(arg, func(path string, f fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if r.skipDir(path, f) {
					return filepath.SkipDir
				}

				if f.IsDir() {
					return nil
				}

				if r.isIgnoredFile(path) {
					return nil
				}

				info, err := f.Info()
				if err != nil {
					s.AddReport(err)

					return nil
				}

				s.Add(fileWeight(path, info), func(rp *reporter) error {
					return r.processFile(path, info, nil, rp)
				})

				return nil
			})
			if err != nil {
				s.AddReport(err)
			}
		}
	}
}

func (r *Runner) processFile(path string, info fs.FileInfo, in io.Reader, rp *reporter) error {
	slog.Debug("processing file", slog.String("path", path))

	content, err := readFile(path, info, in)
	if err != nil {
		return err
	}

	if r.ignoreGenerated && isGenerated(content) {
		return nil
	}

	// Do initial, non-line-length-aware formatting
	result, err := r.extraFormatter.Format(context.Background(), content)
	if err != nil {
		return err
	}

	result, err = r.shortener.Process(result)
	if err != nil {
		return err
	}

	if !r.extraFormatter.IsGofmtCompliant() {
		// Do the final round of non-line-length-aware formatting after we've fixed up the comments
		result, err = r.extraFormatter.Format(context.Background(), result)
		if err != nil {
			return err
		}
	}

	return r.handleOutput(path, content, result, info, rp)
}

// handleOutput generates output according to the value of the tool's
// flags; depending on the latter, the output might be written over
// the source file, printed to stdout, etc.
func (r *Runner) handleOutput(
	filename string,
	src, res []byte,
	info fs.FileInfo,
	rp *reporter,
) error {
	if !r.listFiles && !r.writeOutput && !r.dryRun {
		_, _ = rp.Write(res)
	}

	if bytes.Equal(src, res) {
		slog.Debug("content unchanged, skipping write")

		return nil
	}

	if r.listFiles {
		_, _ = fmt.Fprintln(rp, filename)
	}

	if r.writeOutput {
		if filename == "" {
			return errors.New("no path to write out to")
		}

		slog.Debug("content changed, writing output", slog.String("path", filename))

		return writeFile(filename, src, res, info.Mode().Perm(), info.Size())
	}

	if r.dryRun {
		_, _ = rp.Write(diff.Pretty(filename, src, res))

		return nil
	}

	return nil
}

func deref[T any](v *T) T { //nolint:ireturn
	if v == nil {
		var zero T

		return zero
	}

	return *v
}
