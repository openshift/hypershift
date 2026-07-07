package main

import (
	"fmt"
	"io"
	"testing"
	"time"
)

// mockQuerier provides preconfigured responses for testing.
// For List methods, it returns results from the corresponding slice by call index,
// repeating the last entry for any extra calls.
type mockQuerier struct {
	pipelineRunCalls   int
	pipelineRunResults [][]PipelineRun

	releaseCalls   int
	releaseResults []releaseCallResult

	releasePipelineRunCalls   int
	releasePipelineRunResults [][]PipelineRun

	getReleasePlanFn          func(name string) (*ReleasePlan, error)
	getReleasePlanAdmissionFn func(namespace, name string) (*ReleasePlanAdmission, error)
	getSnapshotFn             func(name string) (*Snapshot, error)
}

type releaseCallResult struct {
	releases []Release
	err      error
}

func (m *mockQuerier) ListPipelineRuns(sha string) ([]PipelineRun, error) {
	idx := m.pipelineRunCalls
	m.pipelineRunCalls++
	if idx < len(m.pipelineRunResults) {
		return m.pipelineRunResults[idx], nil
	}
	if len(m.pipelineRunResults) > 0 {
		return m.pipelineRunResults[len(m.pipelineRunResults)-1], nil
	}
	return nil, nil
}

func (m *mockQuerier) ListReleases(sha string) ([]Release, error) {
	idx := m.releaseCalls
	m.releaseCalls++
	if idx < len(m.releaseResults) {
		return m.releaseResults[idx].releases, m.releaseResults[idx].err
	}
	if len(m.releaseResults) > 0 {
		last := m.releaseResults[len(m.releaseResults)-1]
		return last.releases, last.err
	}
	return nil, nil
}

func (m *mockQuerier) ListReleasePipelineRuns(releaseName string) ([]PipelineRun, error) {
	idx := m.releasePipelineRunCalls
	m.releasePipelineRunCalls++
	if idx < len(m.releasePipelineRunResults) {
		return m.releasePipelineRunResults[idx], nil
	}
	if len(m.releasePipelineRunResults) > 0 {
		return m.releasePipelineRunResults[len(m.releasePipelineRunResults)-1], nil
	}
	return nil, nil
}

func (m *mockQuerier) GetReleasePlan(name string) (*ReleasePlan, error) {
	if m.getReleasePlanFn != nil {
		return m.getReleasePlanFn(name)
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockQuerier) GetReleasePlanAdmission(namespace, name string) (*ReleasePlanAdmission, error) {
	if m.getReleasePlanAdmissionFn != nil {
		return m.getReleasePlanAdmissionFn(namespace, name)
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockQuerier) GetSnapshot(name string) (*Snapshot, error) {
	if m.getSnapshotFn != nil {
		return m.getSnapshotFn(name)
	}
	return nil, fmt.Errorf("not found")
}

func completedPipelineRun(name string) PipelineRun {
	return PipelineRun{
		Metadata: ObjectMeta{Name: name, CreationTimestamp: "2026-07-06T10:00:00Z"},
		Status:   PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}}},
	}
}

func runningPipelineRun(name string) PipelineRun {
	return PipelineRun{
		Metadata: ObjectMeta{Name: name, CreationTimestamp: "2026-07-06T10:00:00Z"},
		Status:   PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Running"}}},
	}
}

func succeededRelease(name string) Release {
	return Release{
		Metadata: ObjectMeta{Name: name, CreationTimestamp: "2026-07-06T10:00:00Z"},
		Status:   ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}}},
	}
}

func progressingRelease(name string) Release {
	return Release{
		Metadata: ObjectMeta{Name: name, CreationTimestamp: "2026-07-06T10:00:00Z"},
		Status:   ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Progressing"}}},
	}
}

func testWatchConfig() WatchConfig {
	return WatchConfig{
		Interval: time.Millisecond,
		SHA:      "abc123",
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}
}

func TestWatchBuildPipeline_ExitsWhenComplete(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{runningPipelineRun("plr-1")},
			{completedPipelineRun("plr-1")},
		},
	}

	err := WatchBuildPipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.pipelineRunCalls != 2 {
		t.Errorf("expected 2 PipelineRun polls, got %d", mock.pipelineRunCalls)
	}
}

func TestWatchReleasePipeline_WaitsForReleaseCreation(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{completedPipelineRun("plr-1")},
		},
		releaseResults: []releaseCallResult{
			{releases: nil},
			{releases: nil},
			{releases: []Release{succeededRelease("rel-1")}},
		},
	}

	err := WatchReleasePipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.releaseCalls < 3 {
		t.Errorf("expected at least 3 release polls (waited for creation), got %d", mock.releaseCalls)
	}
}

func TestWatchReleasePipeline_WaitsForReleaseCreation_WithError(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{completedPipelineRun("plr-1")},
		},
		releaseResults: []releaseCallResult{
			{err: fmt.Errorf("not found")},
			{releases: nil},
			{releases: []Release{succeededRelease("rel-1")}},
		},
	}

	err := WatchReleasePipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.releaseCalls < 3 {
		t.Errorf("expected at least 3 release polls, got %d", mock.releaseCalls)
	}
}

func TestWatchReleasePipeline_ContinuesWhileReleasePending(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{completedPipelineRun("plr-1")},
		},
		releaseResults: []releaseCallResult{
			{releases: []Release{progressingRelease("rel-1")}},
			{releases: []Release{succeededRelease("rel-1")}},
		},
	}

	err := WatchReleasePipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.releaseCalls < 2 {
		t.Errorf("expected at least 2 release polls, got %d", mock.releaseCalls)
	}
}

func TestWatchReleasePipeline_ExitsWhenAllTerminal(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{completedPipelineRun("plr-1")},
		},
		releaseResults: []releaseCallResult{
			{releases: []Release{succeededRelease("rel-1")}},
		},
	}

	err := WatchReleasePipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First iteration: PipelineRuns done + releases done → exit
	// ListReleases is called twice per iteration (once in printReleasePipeline, once in the check)
	// so we just verify the loop terminated quickly
	if mock.pipelineRunCalls > 2 {
		t.Errorf("expected loop to exit quickly, got %d PipelineRun polls", mock.pipelineRunCalls)
	}
}

func TestWatchReleasePipeline_WaitsForBuildsFirst(t *testing.T) {
	mock := &mockQuerier{
		pipelineRunResults: [][]PipelineRun{
			{runningPipelineRun("plr-1")},
			{completedPipelineRun("plr-1")},
		},
		releaseResults: []releaseCallResult{
			{releases: []Release{succeededRelease("rel-1")}},
		},
	}

	err := WatchReleasePipeline(mock, testWatchConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.pipelineRunCalls < 2 {
		t.Errorf("expected at least 2 PipelineRun polls, got %d", mock.pipelineRunCalls)
	}
}
