package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestActionFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   []byte
		want string
	}{
		{
			name: "When id is nil it should return empty",
			id:   nil,
			want: "",
		},
		{
			name: "When id is empty it should return empty",
			id:   []byte{},
			want: "",
		},
		{
			name: "When id is valid it should return the correct path",
			id:   mustDecodeHex(t, "abcdef0123456789"),
			want: filepath.Join("/cache", "ab", "abcdef0123456789-a"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := actionFile("/cache", tt.id)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutputFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   []byte
		want string
	}{
		{
			name: "When id is nil it should return empty",
			id:   nil,
			want: "",
		},
		{
			name: "When id is valid it should return the correct path",
			id:   mustDecodeHex(t, "abcdef0123456789"),
			want: filepath.Join("/cache", "ab", "abcdef0123456789-d"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := outputFile("/cache", tt.id)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func writeCacheEntry(t *testing.T, dir string, actionID, outputID []byte, body []byte) {
	t.Helper()
	aPath := actionFile(dir, actionID)
	dPath := outputFile(dir, outputID)
	if err := os.MkdirAll(filepath.Dir(aPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dPath, body, 0o666); err != nil {
		t.Fatal(err)
	}
	entry := fmt.Sprintf("v1 %s %s %d %d\n",
		hex.EncodeToString(actionID),
		hex.EncodeToString(outputID),
		len(body),
		time.Now().UnixNano(),
	)
	if err := os.WriteFile(aPath, []byte(entry), 0o666); err != nil {
		t.Fatal(err)
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()
	actionID := mustDecodeHex(t, "aaaaaaaaaaaaaaaa")
	outputID := mustDecodeHex(t, "bbbbbbbbbbbbbbbb")
	body := []byte("hello world")

	t.Run("When entry exists it should return hit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeCacheEntry(t, dir, actionID, outputID, body)

		resp, ok := lookup(dir, actionID)
		if !ok {
			t.Fatal("expected hit, got miss")
		}
		if hex.EncodeToString(resp.OutputID) != hex.EncodeToString(outputID) {
			t.Errorf("outputID = %x, want %x", resp.OutputID, outputID)
		}
		if resp.Size != int64(len(body)) {
			t.Errorf("size = %d, want %d", resp.Size, len(body))
		}
		if resp.Time == nil {
			t.Error("expected non-nil Time")
		}
		if resp.DiskPath == "" {
			t.Error("expected non-empty DiskPath")
		}
	})

	t.Run("When entry does not exist it should return miss", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, ok := lookup(dir, actionID)
		if ok {
			t.Fatal("expected miss, got hit")
		}
	})

	t.Run("When action file has wrong format it should return miss", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aPath := actionFile(dir, actionID)
		os.MkdirAll(filepath.Dir(aPath), 0o777)
		os.WriteFile(aPath, []byte("garbage"), 0o666)

		_, ok := lookup(dir, actionID)
		if ok {
			t.Fatal("expected miss for malformed entry")
		}
	})

	t.Run("When data file is missing it should return miss", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aPath := actionFile(dir, actionID)
		os.MkdirAll(filepath.Dir(aPath), 0o777)
		entry := fmt.Sprintf("v1 %s %s 11 %d\n",
			hex.EncodeToString(actionID),
			hex.EncodeToString(outputID),
			time.Now().UnixNano(),
		)
		os.WriteFile(aPath, []byte(entry), 0o666)

		_, ok := lookup(dir, actionID)
		if ok {
			t.Fatal("expected miss when data file is absent")
		}
	})
}

func TestHandleGet(t *testing.T) {
	t.Parallel()
	actionID := mustDecodeHex(t, "1111111111111111")
	outputID := mustDecodeHex(t, "2222222222222222")
	body := []byte("cached output")

	t.Run("When entry is in rw dir it should return hit from rw", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()
		roDir := t.TempDir()
		writeCacheEntry(t, rwDir, actionID, outputID, body)

		req := &request{ID: 42, Command: "get", ActionID: actionID}
		resp := handleGet(req, roDir, rwDir)
		if resp.Miss {
			t.Fatal("expected hit")
		}
		if resp.ID != 42 {
			t.Errorf("ID = %d, want 42", resp.ID)
		}
		if resp.Size != int64(len(body)) {
			t.Errorf("size = %d, want %d", resp.Size, len(body))
		}
	})

	t.Run("When entry is only in ro dir it should return hit from ro", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()
		roDir := t.TempDir()
		writeCacheEntry(t, roDir, actionID, outputID, body)

		req := &request{ID: 7, Command: "get", ActionID: actionID}
		resp := handleGet(req, roDir, rwDir)
		if resp.Miss {
			t.Fatal("expected hit from ro")
		}
		if resp.ID != 7 {
			t.Errorf("ID = %d, want 7", resp.ID)
		}
	})

	t.Run("When entry is in neither dir it should return miss", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()
		roDir := t.TempDir()

		req := &request{ID: 3, Command: "get", ActionID: actionID}
		resp := handleGet(req, roDir, rwDir)
		if !resp.Miss {
			t.Fatal("expected miss")
		}
	})

	t.Run("When roDir is empty string it should skip ro lookup", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()

		req := &request{ID: 5, Command: "get", ActionID: actionID}
		resp := handleGet(req, "", rwDir)
		if !resp.Miss {
			t.Fatal("expected miss")
		}
	})

	t.Run("When rw shadows ro it should prefer rw", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()
		roDir := t.TempDir()
		rwBody := []byte("rw content")
		roBody := []byte("ro content, different size")
		writeCacheEntry(t, rwDir, actionID, outputID, rwBody)
		writeCacheEntry(t, roDir, actionID, outputID, roBody)

		req := &request{ID: 1, Command: "get", ActionID: actionID}
		resp := handleGet(req, roDir, rwDir)
		if resp.Miss {
			t.Fatal("expected hit")
		}
		if resp.Size != int64(len(rwBody)) {
			t.Errorf("size = %d, want %d (rw should shadow ro)", resp.Size, len(rwBody))
		}
	})
}

func TestHandlePut(t *testing.T) {
	t.Parallel()
	actionID := mustDecodeHex(t, "3333333333333333")
	outputID := mustDecodeHex(t, "4444444444444444")
	body := []byte("new output data")

	t.Run("When writing succeeds it should create action and data files", func(t *testing.T) {
		t.Parallel()
		rwDir := t.TempDir()
		req := &request{
			ID:       10,
			Command:  "put",
			ActionID: actionID,
			OutputID: outputID,
			Body:     body,
			BodySize: int64(len(body)),
		}
		resp := handlePut(req, rwDir)
		if resp.Err != "" {
			t.Fatalf("unexpected error: %s", resp.Err)
		}
		if resp.ID != 10 {
			t.Errorf("ID = %d, want 10", resp.ID)
		}
		if resp.DiskPath == "" {
			t.Error("expected non-empty DiskPath")
		}

		data, err := os.ReadFile(resp.DiskPath)
		if err != nil {
			t.Fatalf("reading data file: %v", err)
		}
		if string(data) != string(body) {
			t.Errorf("data = %q, want %q", data, body)
		}

		lookupResp, ok := lookup(rwDir, actionID)
		if !ok {
			t.Fatal("expected lookup to succeed after put")
		}
		if lookupResp.Size != int64(len(body)) {
			t.Errorf("lookup size = %d, want %d", lookupResp.Size, len(body))
		}
	})

	t.Run("When rwDir is unwritable it should return error", func(t *testing.T) {
		t.Parallel()
		req := &request{
			ID:       11,
			Command:  "put",
			ActionID: actionID,
			OutputID: outputID,
			Body:     body,
			BodySize: int64(len(body)),
		}
		resp := handlePut(req, "/proc/nonexistent-gocacheprog-test")
		if resp.Err == "" {
			t.Fatal("expected error for unwritable dir")
		}
		if resp.ID != 11 {
			t.Errorf("ID = %d, want 11", resp.ID)
		}
	})
}

func TestHandleRequest(t *testing.T) {
	t.Parallel()

	t.Run("When command is close it should return empty response", func(t *testing.T) {
		t.Parallel()
		req := &request{ID: 99, Command: "close"}
		resp := handleRequest(req, "", t.TempDir())
		if resp.ID != 99 {
			t.Errorf("ID = %d, want 99", resp.ID)
		}
		if resp.Err != "" {
			t.Errorf("unexpected error: %s", resp.Err)
		}
	})

	t.Run("When command is unknown it should return error", func(t *testing.T) {
		t.Parallel()
		req := &request{ID: 100, Command: "delete"}
		resp := handleRequest(req, "", t.TempDir())
		if resp.Err != "unknown command" {
			t.Errorf("Err = %q, want %q", resp.Err, "unknown command")
		}
	})
}

func TestPutThenGet(t *testing.T) {
	t.Parallel()
	rwDir := t.TempDir()
	actionID := mustDecodeHex(t, "5555555555555555")
	outputID := mustDecodeHex(t, "6666666666666666")
	body := []byte("round trip data")

	putReq := &request{
		ID:       1,
		Command:  "put",
		ActionID: actionID,
		OutputID: outputID,
		Body:     body,
		BodySize: int64(len(body)),
	}
	putResp := handlePut(putReq, rwDir)
	if putResp.Err != "" {
		t.Fatalf("put error: %s", putResp.Err)
	}

	getReq := &request{ID: 2, Command: "get", ActionID: actionID}
	getResp := handleGet(getReq, "", rwDir)
	if getResp.Miss {
		t.Fatal("expected hit after put")
	}
	if getResp.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", getResp.Size, len(body))
	}
	if hex.EncodeToString(getResp.OutputID) != hex.EncodeToString(outputID) {
		t.Errorf("outputID = %x, want %x", getResp.OutputID, outputID)
	}
}

func TestConcurrentPutAndGet(t *testing.T) {
	t.Parallel()
	rwDir := t.TempDir()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			actionID := mustDecodeHex(t, fmt.Sprintf("%016x", i))
			outputID := mustDecodeHex(t, fmt.Sprintf("%016x", i+1000))
			body := []byte(fmt.Sprintf("body-%d", i))

			putReq := &request{
				ID:       int64(i),
				Command:  "put",
				ActionID: actionID,
				OutputID: outputID,
				Body:     body,
				BodySize: int64(len(body)),
			}
			resp := handlePut(putReq, rwDir)
			if resp.Err != "" {
				t.Errorf("put %d error: %s", i, resp.Err)
				return
			}

			getReq := &request{ID: int64(i + 1000), Command: "get", ActionID: actionID}
			getResp := handleGet(getReq, "", rwDir)
			if getResp.Miss {
				t.Errorf("get %d: expected hit after put", i)
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentPutSameOutputID(t *testing.T) {
	t.Parallel()
	rwDir := t.TempDir()
	outputID := mustDecodeHex(t, "7777777777777777")
	body := []byte("shared output content that all writers agree on")

	// Seed one entry so GETs can find it while PUTs overwrite the data file.
	seedAction := mustDecodeHex(t, fmt.Sprintf("%016x", 2000))
	seedReq := &request{
		ID: 0, Command: "put",
		ActionID: seedAction, OutputID: outputID,
		Body: body, BodySize: int64(len(body)),
	}
	if resp := handlePut(seedReq, rwDir); resp.Err != "" {
		t.Fatalf("seed put: %s", resp.Err)
	}

	// Interleave PUTs (different ActionIDs, same OutputID) with GETs that
	// read the data file via DiskPath. Without atomic writes, a PUT's
	// O_TRUNC would momentarily zero the file, causing a reader to see
	// truncated/empty content.
	var wg sync.WaitGroup
	var badReads atomic.Int64
	for i := range 100 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			actionID := mustDecodeHex(t, fmt.Sprintf("%016x", i+2000))
			putReq := &request{
				ID: int64(i), Command: "put",
				ActionID: actionID, OutputID: outputID,
				Body: body, BodySize: int64(len(body)),
			}
			if resp := handlePut(putReq, rwDir); resp.Err != "" {
				t.Errorf("put %d error: %s", i, resp.Err)
			}
		}()
		go func() {
			defer wg.Done()
			getReq := &request{ID: int64(i + 5000), Command: "get", ActionID: seedAction}
			resp := handleGet(getReq, "", rwDir)
			if resp.Miss {
				return
			}
			data, err := os.ReadFile(resp.DiskPath)
			if err != nil {
				return
			}
			if string(data) != string(body) {
				badReads.Add(1)
			}
		}()
	}
	wg.Wait()

	if n := badReads.Load(); n > 0 {
		t.Errorf("got %d reads with corrupted data from concurrent PUT/GET on same OutputID", n)
	}
}
