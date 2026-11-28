package etcd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

// TestEtcdInitScriptSnapshotCleanup executes the real etcd-init script embedded
// by buildEtcdInitContainer and verifies that the downloaded snapshot is
// removed on every exit path.
//
// The init container runs with a read-only root filesystem and the pod-scoped
// tmp-dir emptyDir mounted at /tmp, so a snapshot (routinely multiple
// gigabytes) left behind would be pinned on management-cluster node ephemeral
// storage for the lifetime of the pod. Before the read-only root filesystem it
// lived in the exited init container's writable layer and was freed with it.
//
// The script is run with bash (the etcd image's /bin/sh) with the paths and
// binaries it parameterizes pointing at a temp dir, a mocked curl, and a fake
// etcdutl, following the pattern of the cno rewrite-config script test.
func TestEtcdInitScriptSnapshotCleanup(t *testing.T) {
	t.Parallel()

	// curlMock replaces curl for the script run. It honors "curl -o <path>
	// <url>" by writing CURL_CONTENT to the target and returning CURL_STATUS.
	// When CURL_FAILS_MID_TRANSFER=true it writes a partial file and fails,
	// modeling a download that dies mid-transfer. Invocations are logged to
	// CURL_LOG so tests can assert curl was never reached.
	const curlMock = `
curl() {
  echo "curl $*" >> "${CURL_LOG:-/dev/null}"
  if [ "${CURL_FAILS_MID_TRANSFER:-}" = "true" ]; then
    printf '%s' partial > "$2"
    return 22
  fi
  printf '%s' "${CURL_CONTENT:-}" > "$2"
  return "${CURL_STATUS:-0}"
}
`

	// fakeEtcdutl is installed as ETCDUTL_BIN. "snapshot status" succeeds;
	// "snapshot restore" creates a data dir with member data, or leaves
	// partial output behind and fails when FAKE_ETCDUTL_FAILS=true, modeling
	// a restore that dies mid-way.
	const fakeEtcdutl = `#!/bin/sh
set -eu
mode="$2"
data_dir=""
for arg in "$@"; do
  case "$arg" in
    --data-dir=*) data_dir="${arg#--data-dir=}" ;;
  esac
done
if [ "${mode}" = "restore" ]; then
  if [ "${FAKE_ETCDUTL_FAILS:-false}" = "true" ]; then
    mkdir -p "${data_dir}/member"
    echo partial > "${data_dir}/member/partial"
    exit 1
  fi
  mkdir -p "${data_dir}/member/snap"
  echo "fake db" > "${data_dir}/member/snap/db"
fi
exit 0
`

	type scriptEnv struct {
		snapshotPath string
		dataDir      string
		restoreDir   string
		etcdutlBin   string
		curlLog      string
	}

	newScriptEnv := func(t *testing.T) scriptEnv {
		t.Helper()
		dir := t.TempDir()
		env := scriptEnv{
			snapshotPath: filepath.Join(dir, "snapshot"),
			dataDir:      filepath.Join(dir, "data"),
			restoreDir:   filepath.Join(dir, "restore"),
			etcdutlBin:   filepath.Join(dir, "bin", "etcdutl"),
			curlLog:      filepath.Join(dir, "curl.log"),
		}
		g := NewWithT(t)
		g.Expect(os.MkdirAll(filepath.Dir(env.etcdutlBin), 0755)).To(Succeed(), "failed to create bin dir for fake etcdutl")
		g.Expect(os.WriteFile(env.etcdutlBin, []byte(fakeEtcdutl), 0755)).To(Succeed(), "failed to write fake etcdutl")
		return env
	}

	// runScript executes the embedded etcd-init script (with the curl mock
	// prepended) against the given env, mirroring the container's
	// "/bin/sh -ce <script>" invocation with bash, the etcd image's /bin/sh.
	runScript := func(t *testing.T, env scriptEnv, extraEnv ...string) (string, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "bash", "-ce", curlMock+"\n"+etcdInitScript)
		cmd.Env = append(os.Environ(),
			"SNAPSHOT_PATH="+env.snapshotPath,
			"DATA_DIR="+env.dataDir,
			"RESTORE_DIR="+env.restoreDir,
			"ETCDUTL_BIN="+env.etcdutlBin,
			"ETCDCTL_BIN="+filepath.Join(filepath.Dir(env.etcdutlBin), "etcdctl"),
			"RESTORE_URL_ETCD=https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			"HOSTNAME=etcd-0",
			"HCP_NAMESPACE=hcp-namespace",
			"ETCD_INITIAL_CLUSTER=etcd-0=https://etcd-0.etcd-discovery.hcp-namespace.svc:2380,etcd-1=https://etcd-1.etcd-discovery.hcp-namespace.svc:2380,etcd-2=https://etcd-2.etcd-discovery.hcp-namespace.svc:2380",
			"CURL_LOG="+env.curlLog,
		)
		cmd.Env = append(cmd.Env, extraEnv...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	assertSnapshotRemoved := func(g Gomega, env scriptEnv) {
		g.Expect(env.snapshotPath).NotTo(BeAnExistingFile(),
			"the downloaded snapshot must be removed so it is not pinned on node ephemeral storage for the lifetime of the pod")
	}

	tests := []struct {
		name        string
		extraEnv    []string
		setup       func(t *testing.T, env scriptEnv)
		expectError bool
		validate    func(g Gomega, env scriptEnv, output string)
	}{
		{
			name:        "When the restore succeeds, it should remove the snapshot and move the restored data into place",
			extraEnv:    []string{"CURL_CONTENT=fake-snapshot-data", "CURL_STATUS=0"},
			expectError: false,
			validate: func(g Gomega, env scriptEnv, output string) {
				assertSnapshotRemoved(g, env)
				g.Expect(filepath.Join(env.dataDir, "member", "snap", "db")).To(BeAnExistingFile(),
					"the restored data should be moved into the data directory")
				restored, err := os.ReadFile(filepath.Join(env.dataDir, "member", "snap", "db"))
				g.Expect(err).NotTo(HaveOccurred(), "failed to read the restored data")
				g.Expect(string(restored)).To(Equal("fake db\n"))
				g.Expect(env.restoreDir).NotTo(BeAnExistingFile(),
					"the staging directory should have been moved away")
			},
		},
		{
			name:        "When the downloaded object is an S3 XML error document, it should exit nonzero, log it and remove the snapshot",
			extraEnv:    []string{"CURL_CONTENT=<?xml version=\"1.0\"?><Error><Code>NoSuchKey</Code></Error>", "CURL_STATUS=0"},
			expectError: true,
			validate: func(g Gomega, env scriptEnv, output string) {
				assertSnapshotRemoved(g, env)
				g.Expect(output).To(ContainSubstring("NoSuchKey"),
					"the XML error document should be dumped to the logs")
			},
		},
		{
			name:        "When the restore fails mid-way, it should exit nonzero and remove the snapshot",
			extraEnv:    []string{"CURL_CONTENT=fake-snapshot-data", "CURL_STATUS=0", "FAKE_ETCDUTL_FAILS=true"},
			expectError: true,
			validate: func(g Gomega, env scriptEnv, output string) {
				assertSnapshotRemoved(g, env)
				// The partial restore output is on the data volume and is
				// cleaned up by the script's own "rm -rf" on the next
				// attempt; only the snapshot must not linger in /tmp.
			},
		},
		{
			name:        "When the download fails mid-transfer, it should exit nonzero and remove the partial snapshot",
			extraEnv:    []string{"CURL_FAILS_MID_TRANSFER=true"},
			expectError: true,
			validate: func(g Gomega, env scriptEnv, output string) {
				assertSnapshotRemoved(g, env)
			},
		},
		{
			name:     "When the data directory is already populated, it should exit successfully without downloading a snapshot",
			extraEnv: []string{"CURL_CONTENT=fake-snapshot-data", "CURL_STATUS=0"},
			setup: func(t *testing.T, env scriptEnv) {
				g := NewWithT(t)
				g.Expect(os.MkdirAll(filepath.Join(env.dataDir, "member", "snap"), 0755)).To(Succeed(), "failed to create the existing data directory")
				g.Expect(os.WriteFile(filepath.Join(env.dataDir, "member", "snap", "db"), []byte("existing db"), 0644)).To(Succeed(), "failed to pre-populate the data directory")
			},
			expectError: false,
			validate: func(g Gomega, env scriptEnv, output string) {
				g.Expect(env.snapshotPath).NotTo(BeAnExistingFile(),
					"no snapshot should have been downloaded")
				g.Expect(env.curlLog).NotTo(BeAnExistingFile(),
					"curl should not have been invoked")
				existing, err := os.ReadFile(filepath.Join(env.dataDir, "member", "snap", "db"))
				g.Expect(err).NotTo(HaveOccurred(), "failed to read the existing data")
				g.Expect(string(existing)).To(Equal("existing db"),
					"the existing data must be left untouched")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			env := newScriptEnv(t)
			if test.setup != nil {
				test.setup(t, env)
			}
			output, err := runScript(t, env, test.extraEnv...)
			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "expected the script to exit nonzero, output:\n%s", output)
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "expected the script to succeed, output:\n%s", output)
			}
			test.validate(g, env, output)
		})
	}
}
