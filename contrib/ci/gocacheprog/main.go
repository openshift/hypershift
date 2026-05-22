// gocacheprog implements the GOCACHEPROG protocol (Go 1.24+) to serve
// a read-only Go build cache with a writable overlay. GET requests are
// served from a writable local directory first, then from a read-only
// shared directory (e.g. an EFS-backed PVC). PUT requests always write
// to the local directory. This eliminates the need to copy the shared
// cache at job start.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type request struct {
	ID       int64  `json:"ID"`
	Command  string `json:"Command"`
	ActionID []byte `json:"ActionID,omitempty"`
	OutputID []byte `json:"OutputID,omitempty"`
	Body     []byte `json:"-"`
	BodySize int64  `json:"BodySize,omitempty"`
}

type response struct {
	ID            int64      `json:"ID"`
	Err           string     `json:"Err,omitempty"`
	KnownCommands []string   `json:"KnownCommands,omitempty"`
	Miss          bool       `json:"Miss,omitempty"`
	OutputID      []byte     `json:"OutputID,omitempty"`
	Size          int64      `json:"Size,omitempty"`
	Time          *time.Time `json:"Time,omitempty"`
	DiskPath      string     `json:"DiskPath,omitempty"`
}

func main() {
	roDir := flag.String("ro", "", "read-only cache directory (e.g. EFS mount)")
	rwDir := flag.String("rw", "", "writable cache directory (e.g. /tmp/go-build-cache)")
	flag.Parse()

	if *rwDir == "" {
		fmt.Fprintln(os.Stderr, "gocacheprog: --rw is required")
		os.Exit(1)
	}

	jd := json.NewDecoder(os.Stdin)
	je := json.NewEncoder(os.Stdout)
	var mu sync.Mutex

	je.Encode(response{KnownCommands: []string{"get", "put", "close"}})

	for {
		var req request
		if err := jd.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			log.Fatalf("gocacheprog: decode request: %v", err)
		}

		if req.Command == "put" && req.BodySize > 0 {
			if err := jd.Decode(&req.Body); err != nil {
				log.Fatalf("gocacheprog: decode body: %v", err)
			}
		}

		go func() {
			res := handleRequest(&req, *roDir, *rwDir)
			mu.Lock()
			je.Encode(res)
			mu.Unlock()
		}()
	}
}

func handleRequest(req *request, roDir, rwDir string) response {
	switch req.Command {
	case "get":
		return handleGet(req, roDir, rwDir)
	case "put":
		return handlePut(req, rwDir)
	case "close":
		return response{ID: req.ID}
	default:
		return response{ID: req.ID, Err: "unknown command"}
	}
}

// actionFile returns the path to a Go cache action entry.
// Format: <dir>/<first-byte-hex>/<full-hex-actionID>-a
func actionFile(dir string, id []byte) string {
	h := hex.EncodeToString(id)
	return filepath.Join(dir, h[:2], h+"-a")
}

// outputFile returns the path to a Go cache data file.
// Format: <dir>/<first-byte-hex>/<full-hex-outputID>-d
func outputFile(dir string, id []byte) string {
	h := hex.EncodeToString(id)
	return filepath.Join(dir, h[:2], h+"-d")
}

// lookup reads a Go cache action entry and verifies the data file exists.
// The action entry format is: v1 <hexActionID> <hexOutputID> <size> <unixnanos>
func lookup(dir string, actionID []byte) (resp response, ok bool) {
	data, err := os.ReadFile(actionFile(dir, actionID))
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) != 5 || fields[0] != "v1" {
		return
	}
	if fields[1] != hex.EncodeToString(actionID) {
		return
	}
	outputID, err := hex.DecodeString(fields[2])
	if err != nil {
		return
	}
	nanos, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return
	}
	dPath := outputFile(dir, outputID)
	fi, err := os.Stat(dPath)
	if err != nil {
		return
	}
	t := time.Unix(0, nanos)
	return response{
		OutputID: outputID,
		Size:     fi.Size(),
		Time:     &t,
		DiskPath: dPath,
	}, true
}

func handleGet(req *request, roDir, rwDir string) response {
	if resp, ok := lookup(rwDir, req.ActionID); ok {
		resp.ID = req.ID
		return resp
	}
	if roDir != "" {
		if resp, ok := lookup(roDir, req.ActionID); ok {
			resp.ID = req.ID
			return resp
		}
	}
	return response{ID: req.ID, Miss: true}
}

func handlePut(req *request, rwDir string) response {
	dPath := outputFile(rwDir, req.OutputID)
	os.MkdirAll(filepath.Dir(dPath), 0o777)
	if err := os.WriteFile(dPath, req.Body, 0o666); err != nil {
		return response{ID: req.ID, Err: err.Error()}
	}

	aPath := actionFile(rwDir, req.ActionID)
	os.MkdirAll(filepath.Dir(aPath), 0o777)
	entry := fmt.Sprintf("v1 %s %s %d %d\n",
		hex.EncodeToString(req.ActionID),
		hex.EncodeToString(req.OutputID),
		len(req.Body),
		time.Now().UnixNano(),
	)
	if err := os.WriteFile(aPath, []byte(entry), 0o666); err != nil {
		return response{ID: req.ID, Err: err.Error()}
	}

	return response{ID: req.ID, DiskPath: dPath}
}

