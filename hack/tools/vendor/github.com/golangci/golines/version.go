package main

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
	"strings"
)

type BuildInfo struct {
	GoVersion string `json:"goVersion"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
}

func (b BuildInfo) String() string {
	return fmt.Sprintf("golines has version %s built with %s from %s on %s",
		b.Version, b.GoVersion, b.Commit, b.Date)
}

func createBuildInfo() BuildInfo {
	info := BuildInfo{
		Commit:    commit,
		Version:   version,
		GoVersion: "unknown",
		Date:      date,
	}

	buildInfo, available := debug.ReadBuildInfo()
	if !available {
		return info
	}

	info.GoVersion = buildInfo.GoVersion

	if date != "" {
		return info
	}

	info.Version = buildInfo.Main.Version

	matched, _ := regexp.MatchString(`v\d+\.\d+\.\d+`, buildInfo.Main.Version)
	if matched {
		info.Version = strings.TrimPrefix(buildInfo.Main.Version, "v")
	}

	var (
		revision string
		modified string
	)

	for _, setting := range buildInfo.Settings {
		// The `vcs.xxx` information is only available with `go build` and `go install`.
		// This information is not available with `go run`.
		switch setting.Key {
		case "vcs.time":
			info.Date = setting.Value
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}

	info.Date = cmp.Or(info.Date, "(unknown)")

	info.Commit = fmt.Sprintf("(%s, modified: %s, mod sum: %q)",
		cmp.Or(revision, "unknown"), cmp.Or(modified, "?"), buildInfo.Main.Sum)

	return info
}

func printVersion(w io.Writer) {
	fmt.Fprintln(w, createBuildInfo().String())
}
