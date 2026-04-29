//go:build eval

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	claudeTimeout = 10 * time.Minute
	testdataDir   = "testdata"
	promptFile    = "prompt.txt"
	expectedFile  = "expected.txt"
	patchFile     = "patch.diff"

	sonnetModel = "claude-sonnet-4-6"
	opusModel   = "claude-opus-4-6"
	haikuModel  = "claude-haiku-4-5-20251001"

	defaultModel      = opusModel
	defaultJudgeModel = opusModel
	defaultThreshold  = 0.8

	judgePromptTemplate = `You are a judge evaluating an agent output against expected issues.

Agent output:
%s

Expected issues (one per line):
%s

Compare using SEMANTIC matching - focus on whether the same fundamental problems were identified, not exact wording.

You should return pass=true if ALL expected issues are semantically covered in the output. The same core problem must be identified, even if described differently, bundled with other issues, or split into sub-items.

An expected issue counts as covered if the output addresses it ANYWHERE — as a numbered issue, inside another issue's discussion, in a corrected code example, or in a summary table. It does NOT need to be a standalone finding.

The output must NOT report issues that are entirely unrelated to any expected issue. However, expanding on an expected issue is OK (e.g., adding MaxLength when the expected issue is about validation). Bundling multiple expected issues into a single finding is also OK.

If the expected issues list is EMPTY, the agent should report NO issues. Return pass=true only if the output confirms the API change looks correct or has no significant problems. Return pass=false if the output reports any issues — this means the agent is hallucinating problems in clean code.

Examples of semantic matches:
- "missing FeatureGate" matches "needs FeatureGate and must register it in features.go"
- "optional field missing omitted behavior" matches "field does not document what happens when not specified"
- "references cpov2 framework" matches "use the controlPlaneComponent reconciliation pattern"
- "missing +optional markers" matches a corrected code example that adds +optional even if not called out as a separate issue

You MUST respond with ONLY a raw JSON object. Do NOT wrap in markdown code blocks. Do NOT include any other text.
{
  "pass": true or false,
  "issues": [
    {"issue": "expected issue text", "covered": true, "reason": "how it was covered"},
    {"issue": "expected issue text", "covered": false, "reason": "why it was not covered"}
  ]
}
pass is true only if ALL issues have covered=true AND no entirely unrelated issues were reported.`
)

type testCase struct {
	Agent          string
	Name           string
	Prompt         string
	Patch          []byte
	ExpectedIssues string
}

type testCaseResult struct {
	Name     string
	Passed   int
	Runs     int
	Rate     float64
	Failures []string
}

type claudeOutput struct {
	Type         string  `json:"type"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

type issueVerdict struct {
	Issue   string `json:"issue"`
	Covered bool   `json:"covered"`
	Reason  string `json:"reason"`
}

type judgeResult struct {
	Pass   bool           `json:"pass"`
	Issues []issueVerdict `json:"issues"`
}

var (
	repoRoot       string
	evalModel      string
	judgeModel     string
	evalRuns       int
	evalThreshold  float64
	totalAgentCost float64
	totalJudgeCost float64
	allResults     []testCaseResult
)

func TestEval(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Eval Suite")
}

func envOrDefault(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

var _ = BeforeSuite(func() {
	evalModel = envOrDefault("EVAL_MODEL", defaultModel)
	judgeModel = envOrDefault("EVAL_JUDGE_MODEL", defaultJudgeModel)

	var err error
	evalRuns, err = strconv.Atoi(envOrDefault("EVAL_RUNS", "1"))
	Expect(err).NotTo(HaveOccurred(), "EVAL_RUNS must be an integer")
	Expect(evalRuns).To(BeNumerically(">", 0), "EVAL_RUNS must be positive")

	evalThreshold, err = strconv.ParseFloat(envOrDefault("EVAL_THRESHOLD", fmt.Sprintf("%g", defaultThreshold)), 64)
	Expect(err).NotTo(HaveOccurred(), "EVAL_THRESHOLD must be a float")

	repoRoot, err = filepath.Abs(filepath.Join("..", ".."))
	Expect(err).NotTo(HaveOccurred())

	By("verifying agents directory exists")
	_, err = os.Stat(filepath.Join(repoRoot, ".claude", "agents"))
	Expect(err).NotTo(HaveOccurred(), ".claude/agents/ must exist in repository root")
})

var _ = AfterSuite(func() {
	if len(allResults) > 0 {
		fmt.Printf("\n=== Eval Results (threshold: %.0f%%) ===\n\n", evalThreshold*100)
		for _, r := range allResults {
			status := "PASS"
			if r.Rate < evalThreshold {
				status = "FAIL"
			}
			fmt.Printf("  - [%s] %s — %d/%d passed (%.0f%%)\n", status, r.Name, r.Passed, r.Runs, r.Rate*100)
			for _, f := range r.Failures {
				fmt.Printf("      - %s\n", f)
			}
		}
		fmt.Println()
	}

	fmt.Printf("Total Cost: $%.4f (Agent: $%.4f, Judge: $%.4f)\n",
		totalAgentCost+totalJudgeCost, totalAgentCost, totalJudgeCost)
})

func loadScenario(dir, name, agent string) testCase {
	prompt, err := os.ReadFile(filepath.Join(dir, promptFile))
	Expect(err).NotTo(HaveOccurred(), "prompt.txt missing in %s", name)

	expected, err := os.ReadFile(filepath.Join(dir, expectedFile))
	Expect(err).NotTo(HaveOccurred(), "expected.txt missing in %s", name)

	var patch []byte
	if data, err := os.ReadFile(filepath.Join(dir, patchFile)); err == nil {
		patch = data
	}

	return testCase{
		Agent:          agent,
		Name:           name,
		Prompt:         strings.TrimSpace(string(prompt)),
		Patch:          patch,
		ExpectedIssues: strings.TrimSpace(string(expected)),
	}
}

func discoverTestCases(baseDir string) []testCase {
	topDirs, err := os.ReadDir(baseDir)
	Expect(err).NotTo(HaveOccurred(), "failed to read testdata directory")

	var cases []testCase
	for _, topEntry := range topDirs {
		if !topEntry.IsDir() {
			continue
		}
		topName := topEntry.Name()
		topPath := filepath.Join(baseDir, topName)

		if topName == "sme-agents" {
			// sme-agents/<agent-name>/<scenario>/ — three levels, agent from dir name
			agentDirs, err := os.ReadDir(topPath)
			Expect(err).NotTo(HaveOccurred())
			for _, agentEntry := range agentDirs {
				if !agentEntry.IsDir() {
					continue
				}
				agentName := agentEntry.Name()
				scenarioDirs, err := os.ReadDir(filepath.Join(topPath, agentName))
				Expect(err).NotTo(HaveOccurred())
				for _, scenarioEntry := range scenarioDirs {
					if !scenarioEntry.IsDir() {
						continue
					}
					name := fmt.Sprintf("%s/%s/%s", topName, agentName, scenarioEntry.Name())
					dir := filepath.Join(topPath, agentName, scenarioEntry.Name())
					cases = append(cases, loadScenario(dir, name, agentName))
				}
			}
		} else {
			// <category>/<scenario>/ — two levels, no agent
			scenarioDirs, err := os.ReadDir(topPath)
			Expect(err).NotTo(HaveOccurred())
			for _, scenarioEntry := range scenarioDirs {
				if !scenarioEntry.IsDir() {
					continue
				}
				name := fmt.Sprintf("%s/%s", topName, scenarioEntry.Name())
				dir := filepath.Join(topPath, scenarioEntry.Name())
				cases = append(cases, loadScenario(dir, name, ""))
			}
		}
	}
	return cases
}

func applyPatch(patch []byte) {
	cleanupRepo()

	By("applying patch")
	cmd := exec.Command("git", "apply", "-")
	cmd.Dir = repoRoot
	cmd.Stdin = bytes.NewReader(patch)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git apply failed: %s", string(output))
}

func cleanupRepo() {
	By("cleaning up repo")
	cmd := exec.Command("git", "checkout", "--", ".")
	cmd.Dir = repoRoot
	cmd.CombinedOutput()

	cmd = exec.Command("git", "clean", "-fd", "--", "api/")
	cmd.Dir = repoRoot
	cmd.CombinedOutput()
}

func runAgent(tc testCase, model string) (string, float64) {
	By(fmt.Sprintf("running agent %s via Claude (%s)", tc.Agent, model))
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--model", model,
		"--output-format", "json",
		"--no-session-persistence",
		"-p", tc.Prompt,
	}

	if tc.Agent != "" {
		args = append(args, "--agent", tc.Agent)
	}

	if tc.Patch != nil {
		args = append(args, "--allowed-tools", "Bash,Read,Grep,Glob")
	} else {
		args = append(args, "--allowed-tools", "")
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "claude command failed: %s", string(output))

	var parsed claudeOutput
	err = json.Unmarshal(output, &parsed)
	Expect(err).NotTo(HaveOccurred(), "failed to parse claude output: %s", string(output))

	totalAgentCost += parsed.TotalCostUSD
	return parsed.Result, parsed.TotalCostUSD
}

func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func runJudge(model, agentOutput, expectedIssues string) (judgeResult, float64) {
	By(fmt.Sprintf("judging output with Claude (%s)", model))
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	prompt := fmt.Sprintf(judgePromptTemplate, agentOutput, expectedIssues)
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--dangerously-skip-permissions",
		"--model", model,
		"--output-format", "json",
		"--no-session-persistence",
		"-p", prompt,
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "claude judge command failed: %s", string(output))

	var parsed claudeOutput
	err = json.Unmarshal(output, &parsed)
	Expect(err).NotTo(HaveOccurred(), "failed to parse judge output: %s", string(output))

	totalJudgeCost += parsed.TotalCostUSD

	var result judgeResult
	jsonStr := stripMarkdownCodeBlock(parsed.Result)
	err = json.Unmarshal([]byte(jsonStr), &result)
	Expect(err).NotTo(HaveOccurred(), "failed to parse judge response as JSON: %s", parsed.Result)
	return result, parsed.TotalCostUSD
}

func runTestCase(tc testCase) {
	result := testCaseResult{Name: tc.Name, Runs: evalRuns}

	if tc.Patch != nil {
		applyPatch(tc.Patch)
		DeferCleanup(func() { cleanupRepo() })
	}

	for i := range evalRuns {
		By(fmt.Sprintf("run %d/%d", i+1, evalRuns))

		agentOutput, agentCost := runAgent(tc, evalModel)

		GinkgoWriter.Printf("\n--- Agent Output (run %d/%d) ---\n%s\n--- End Agent Output ---\n\n",
			i+1, evalRuns, agentOutput)

		judge, judgeCost := runJudge(judgeModel, agentOutput, tc.ExpectedIssues)

		GinkgoWriter.Printf("Run %d/%d: pass=%v, Agent=$%.4f, Judge=$%.4f\n",
			i+1, evalRuns, judge.Pass, agentCost, judgeCost)
		for _, iv := range judge.Issues {
			status := "COVERED"
			if !iv.Covered {
				status = "MISSED"
			}
			GinkgoWriter.Printf("  [%s] %s — %s\n", status, iv.Issue, iv.Reason)
		}

		if judge.Pass {
			result.Passed++
		} else {
			var missed []string
			for _, iv := range judge.Issues {
				if !iv.Covered {
					missed = append(missed, fmt.Sprintf("[MISSED] %s — %s", iv.Issue, iv.Reason))
				}
			}
			result.Failures = append(result.Failures, fmt.Sprintf("run %d:\n      %s", i+1, strings.Join(missed, "\n      ")))
		}
	}

	result.Rate = float64(result.Passed) / float64(result.Runs)
	allResults = append(allResults, result)

	GinkgoWriter.Printf("Result: %d/%d passed (%.0f%%), threshold: %.0f%%\n",
		result.Passed, result.Runs, result.Rate*100, evalThreshold*100)

	failureList := ""
	for _, f := range result.Failures {
		failureList += fmt.Sprintf("  - %s\n", f)
	}
	Expect(result.Rate).To(BeNumerically(">=", evalThreshold),
		"pass rate %.0f%% below threshold %.0f%% for %s.\nFailures:\n%s",
		result.Rate*100, evalThreshold*100, tc.Name, failureList)
}

var _ = Describe("Agent Evaluation", func() {
	cwd, _ := os.Getwd()
	cases := discoverTestCases(filepath.Join(cwd, testdataDir))

	for _, tc := range cases {
		tc := tc
		It(tc.Name, func() {
			runTestCase(tc)
		})
	}
})
