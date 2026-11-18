package etcdsanfix

import "testing"

func TestVerdict(t *testing.T) {
	tests := []struct {
		name   string
		checks []check
		want   string
	}{
		{"all pass", []check{{status: "PASS"}, {status: "PASS"}}, "GO"},
		{"warn and info do not fail", []check{{status: "PASS"}, {status: "WARN"}, {status: "INFO"}}, "GO"},
		{"any fail fails", []check{{status: "PASS"}, {status: "FAIL"}, {status: "WARN"}}, "NO-GO"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdict(tc.checks, "GO", "NO-GO"); got != tc.want {
				t.Errorf("verdict = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBoolCheck(t *testing.T) {
	if c := boolCheck("x", true, "d"); c.status != "PASS" {
		t.Errorf("boolCheck(true) status = %q, want PASS", c.status)
	}
	if c := boolCheck("x", false, "d"); c.status != "FAIL" {
		t.Errorf("boolCheck(false) status = %q, want FAIL", c.status)
	}
}
