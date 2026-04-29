//go:build eval

package eval

import (
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
	claudeTimeout = 5 * time.Minute
	testdataDir   = "testdata"
	promptFile    = "prompt.txt"
	expectedFile  = "expected.txt"

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

You should return pass=true ONLY if BOTH conditions are met:
1. ALL expected issues are semantically covered in the output (the same core problem is identified, even if described differently or split into sub-items)
2. NO unrelated issues are reported - if the output identifies a problem that is NOT semantically related to any expected issue, you should return pass=false

Expanding on an expected issue is OK (e.g., "missing FeatureGate" expanding to include "register in features.go").
Reporting an entirely different issue is NOT OK (e.g., if "missing length validation" is not in expected list, you should return pass=false).

Examples of semantic matches:
- "missing FeatureGate" matches "needs FeatureGate and must register it in features.go"
- "optional field missing omitted behavior" matches "field does not document what happens when not specified"
- "references cpov2 framework" matches "use the controlPlaneComponent reconciliation pattern"

You MUST respond with ONLY a raw JSON object. Do NOT wrap in markdown code blocks. Do NOT include any other text.
{"pass": true, "reason": "Brief summary of matched issues"}
or
{"pass": false, "reason": "Explanation of what was missing or what unexpected issue was found"}`
)

type testCase struct {
	Agent          string
	Name           string
	Prompt         string
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

type judgeResult struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
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
		fmt.Printf("\n%-50s | %6s | %4s | %s\n", "Test Case", "Passed", "Runs", "Rate")
		fmt.Printf("%s\n", strings.Repeat("-", 75))
		for _, r := range allResults {
			line := fmt.Sprintf("%-50s | %6d | %4d | %3.0f%%", r.Name, r.Passed, r.Runs, r.Rate*100)
			if r.Rate < evalThreshold {
				line += " <- FAIL"
			}
			fmt.Println(line)
		}
		fmt.Printf("\nThreshold: %.0f%%\n", evalThreshold*100)
	}

	fmt.Printf("Total Cost: $%.4f (Agent: $%.4f, Judge: $%.4f)\n",
		totalAgentCost+totalJudgeCost, totalAgentCost, totalJudgeCost)
})

func discoverTestCases(baseDir string) []testCase {
	agentDirs, err := os.ReadDir(baseDir)
	Expect(err).NotTo(HaveOccurred(), "failed to read testdata directory")

	var cases []testCase
	for _, agentEntry := range agentDirs {
		if !agentEntry.IsDir() {
			continue
		}
		agentName := agentEntry.Name()

		scenarioDirs, err := os.ReadDir(filepath.Join(baseDir, agentName))
		Expect(err).NotTo(HaveOccurred())

		for _, scenarioEntry := range scenarioDirs {
			if !scenarioEntry.IsDir() {
				continue
			}

			prompt, err := os.ReadFile(filepath.Join(baseDir, agentName, scenarioEntry.Name(), promptFile))
			Expect(err).NotTo(HaveOccurred(), "prompt.txt missing in %s/%s", agentName, scenarioEntry.Name())

			expected, err := os.ReadFile(filepath.Join(baseDir, agentName, scenarioEntry.Name(), expectedFile))
			Expect(err).NotTo(HaveOccurred(), "expected.txt missing in %s/%s", agentName, scenarioEntry.Name())

			cases = append(cases, testCase{
				Agent:          agentName,
				Name:           fmt.Sprintf("%s/%s", agentName, scenarioEntry.Name()),
				Prompt:         strings.TrimSpace(string(prompt)),
				ExpectedIssues: strings.TrimSpace(string(expected)),
			})
		}
	}
	return cases
}

func loadEntries() []TableEntry {
	cwd, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())

	cases := discoverTestCases(filepath.Join(cwd, testdataDir))
	var entries []TableEntry
	for _, tc := range cases {
		entries = append(entries, Entry(tc.Name, tc))
	}
	return entries
}

func runAgent(tc testCase, model string) (string, float64) {
	By(fmt.Sprintf("running agent %s via Claude (%s)", tc.Agent, model))
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--dangerously-skip-permissions",
		"--model", model,
		"--agent", tc.Agent,
		"--allowed-tools", "",
		"--output-format", "json",
		"--no-session-persistence",
		"-p", tc.Prompt,
	)
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

	for i := range evalRuns {
		By(fmt.Sprintf("run %d/%d", i+1, evalRuns))

		agentOutput, agentCost := runAgent(tc, evalModel)

		GinkgoWriter.Printf("\n--- Agent Output (run %d/%d) ---\n%s\n--- End Agent Output ---\n\n",
			i+1, evalRuns, agentOutput)

		judge, judgeCost := runJudge(judgeModel, agentOutput, tc.ExpectedIssues)

		GinkgoWriter.Printf("Run %d/%d: pass=%v, Agent=$%.4f, Judge=$%.4f\n",
			i+1, evalRuns, judge.Pass, agentCost, judgeCost)
		GinkgoWriter.Printf("Judge reason: %s\n", judge.Reason)

		if judge.Pass {
			result.Passed++
		} else {
			result.Failures = append(result.Failures, fmt.Sprintf("run %d: %s", i+1, judge.Reason))
		}
	}

	result.Rate = float64(result.Passed) / float64(result.Runs)
	allResults = append(allResults, result)

	GinkgoWriter.Printf("Result: %d/%d passed (%.0f%%), threshold: %.0f%%\n",
		result.Passed, result.Runs, result.Rate*100, evalThreshold*100)

	Expect(result.Rate).To(BeNumerically(">=", evalThreshold),
		"pass rate %.0f%% below threshold %.0f%% for %s.\nFailures:\n%s",
		result.Rate*100, evalThreshold*100, tc.Name, strings.Join(result.Failures, "\n"))
}

var _ = Describe("Agent Evaluation", func() {
	Context("When evaluating SME agents", func() {
		DescribeTable("it should correctly address expected issues",
			func(tc testCase) {
				runTestCase(tc)
			},
			loadEntries(),
		)
	})
})
