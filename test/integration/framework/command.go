package framework

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

func RunCommand(logger logr.Logger, opts *Options, logPath string, cmd *exec.Cmd) error {
	logFile, cmd, formattedCommand, err := setupCommand(opts, logPath, cmd)
	if err != nil {
		return err
	}
	once := sync.Once{}
	closeFile := func() {
		if err := logFile.Close(); err != nil {
			logger.Error(err, "failed to close log file")
		}
	}
	defer func() {
		once.Do(closeFile)
	}()

	start := time.Now()
	logger = logger.WithValues("cmd", formattedCommand, "output", logPath)
	logger.Info("running command")
	err = cmd.Run()
	duration := time.Since(start)
	logger = logger.WithValues("error", err != nil, "duration", duration)
	logger.Info("ran command")
	if err != nil {
		once.Do(closeFile)
		output, err := os.ReadFile(filepath.Join(opts.ArtifactDir, logPath))
		if err != nil {
			logger.Error(err, "couldn't read command output")
		}
		fmt.Println(string(output))
	}
	return err
}

func StartCommand(logger logr.Logger, opts *Options, logPath string, cmd *exec.Cmd) error {
	logFile, cmd, formattedCommand, err := setupCommand(opts, logPath, cmd)
	if err != nil {
		return err
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			logger.Error(err, "failed to close log file")
		}
	}()

	logger = logger.WithValues("cmd", formattedCommand, "output", logPath)
	logger.Info("starting command")
	err = cmd.Start()

	go func() {
		if err := cmd.Wait(); err != nil {
			logger.Error(err, "error waiting for long-running command")
		}
	}()
	return err
}

func setupCommand(opts *Options, logPath string, cmd *exec.Cmd) (*os.File, *exec.Cmd, string, error) {
	var formattedArgs []string
	for i, arg := range cmd.Args {
		if i == 0 {
			continue // ignore the executable, we use cmd.Path
		}
		formattedArgs = append(formattedArgs, fmt.Sprintf("'%s'", arg))
	}
	formattedCommand := fmt.Sprintf("%s %v", cmd.Path, strings.Join(formattedArgs, " "))

	logFile, err := Artifact(opts, logPath)
	if err != nil {
		return nil, nil, "", err
	}
	if cmd.Stdout != nil {
		stdout := cmd.Stdout
		cmd.Stdout = io.MultiWriter(stdout, logFile)
	} else {
		cmd.Stdout = logFile
	}
	if cmd.Stderr != nil {
		stderr := cmd.Stderr
		cmd.Stderr = io.MultiWriter(stderr, logFile)
	} else {
		cmd.Stderr = logFile
	}

	for _, env := range cmd.Env {
		if _, err := fmt.Fprintf(logFile, "$ export %s\n", env); err != nil {
			return nil, nil, "", err
		}
	}
	if cmd.Dir != "" {
		if _, err := fmt.Fprintf(logFile, "$ cd %s\n", cmd.Dir); err != nil {
			return nil, nil, "", err
		}
	}
	if _, err := fmt.Fprintf(logFile, "$ %s\n", formattedCommand); err != nil {
		return nil, nil, "", err
	}
	return logFile, cmd, formattedCommand, nil
}
