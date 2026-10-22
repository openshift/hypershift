//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type nativeCorruptionReader struct {
	crclient.Reader
	get  func(context.Context, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error
	list func(context.Context, crclient.ObjectList, ...crclient.ListOption) error
}

func (reader nativeCorruptionReader) Get(ctx context.Context, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
	return reader.get(ctx, key, object, options...)
}

func (reader nativeCorruptionReader) List(ctx context.Context, list crclient.ObjectList, options ...crclient.ListOption) error {
	return reader.list(ctx, list, options...)
}

type nativeCorruptionTestCase struct {
	name            string
	baseline        func(*corev1.PodList)
	observed        func(*corev1.PodList)
	job             func(int, *batchv1.Job) *batchv1.Job
	listErrorAt     int
	jobErrorAt      int
	execError       error
	execOutput      string
	cancelAt        string
	wantError       string
	wantCause       error
	beforeExec      bool
	delayWitness    bool
	sameObservation bool
}

func nativeCorruptionTestCases(sentinel error) []nativeCorruptionTestCase {
	return []nativeCorruptionTestCase{
		{name: "When the selected same UID newly waits after a restart, it should require a subsequent HC owned active Job"},
		{name: "When the detector already created a Job before we observe Waiting, it should accept the same observation with baseline absent identity", sameObservation: true},
		{name: "When the member initially remains healthy, it should wait for its failure before the new Job", delayWitness: true},
		{name: "When a terminal Job predates corruption, it should require a different Job UID", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call == 1 {
				job.UID = "previous-job"
				job.Status = batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}
				return job
			}
			if call == 2 {
				return nil
			}
			return job
		}},
		{name: "When the baseline Job is active, it should reject attribution before exec", beforeExec: true, wantError: "already exists", job: func(call int, job *batchv1.Job) *batchv1.Job { return job }},
		{name: "When the baseline Job is pending, it should reject attribution before exec", beforeExec: true, wantError: "already exists", job: func(call int, job *batchv1.Job) *batchv1.Job { job.Status.Active = 0; return job }},
		{name: "When a terminal Job has no UID, it should reject ambiguous baseline identity", beforeExec: true, wantError: "already exists", job: func(call int, job *batchv1.Job) *batchv1.Job {
			job.UID = ""
			job.Status = batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}
			return job
		}},
		{name: "When the old terminal Job becomes active, it should reject the stale Job", wantError: "previous recovery Job", job: func(call int, job *batchv1.Job) *batchv1.Job {
			job.UID = "previous-job"
			if call == 1 {
				job.Status = batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}
			}
			return job
		}},
		{name: "When another member already qualifies for recovery, it should reject before exec", beforeExec: true, wantError: "other member", baseline: func(pods *corev1.PodList) {
			pods.Items[1].Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}
		}},
		{name: "When another member fails after corruption, it should reject wrong member attribution", wantError: "other member", observed: func(pods *corev1.PodList) {
			pods.Items[1].Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}
		}},
		{name: "When the selected member already waits, it should reject a preexisting failure", beforeExec: true, wantError: "must be Running", baseline: func(pods *corev1.PodList) {
			pods.Items[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}
		}},
		{name: "When Waiting has no restart increase, it should not accept a recovery Job", wantError: "preceded selected member", observed: func(pods *corev1.PodList) { pods.Items[0].Status.ContainerStatuses[0].RestartCount = 2 }},
		{name: "When Waiting has zero restarts, it should not accept a recovery Job", wantError: "preceded selected member", observed: func(pods *corev1.PodList) { pods.Items[0].Status.ContainerStatuses[0].RestartCount = 0 }},
		{name: "When restarts increase without Waiting, it should not accept a recovery Job", wantError: "preceded selected member", observed: func(pods *corev1.PodList) {
			pods.Items[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
		}},
		{name: "When only a sidecar waits, it should not invent an etcd failure witness", wantError: "preceded selected member", observed: func(pods *corev1.PodList) {
			pods.Items[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
			pods.Items[0].Status.ContainerStatuses = append(pods.Items[0].Status.ContainerStatuses, corev1.ContainerStatus{Name: "sidecar", RestartCount: 3, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}})
		}},
		{name: "When the selected Pod is replaced, it should reject reset member identity", wantError: "UID changed", observed: func(pods *corev1.PodList) { pods.Items[0].UID = "replacement" }},
		{name: "When the chosen Pod is missing, it should reject another member as its witness", wantError: "is absent", observed: func(pods *corev1.PodList) {
			pods.Items[0].Name = "etcd-unrelated"
			pods.Items[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
		}},
		{name: "When fewer than three members remain, it should reject incomplete attribution", wantError: "three etcd", observed: func(pods *corev1.PodList) { pods.Items = pods.Items[:2] }},
		{name: "When an etcd status is absent, it should reject unknown eligibility", wantError: "lacks etcd status", observed: func(pods *corev1.PodList) { pods.Items[1].Status.ContainerStatuses = nil }},
		{name: "When a new Job lacks a UID, it should reject ambiguous freshness", wantError: "lacks a UID", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.UID = ""
			return job
		}},
		{name: "When server timestamps precede local observations, it should use distinct observed identity instead of comparing clocks", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
			return job
		}},
		{name: "When a distinct Job has no creation time, it should rely on actual observation ordering", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.CreationTimestamp = metav1.Time{}
			return job
		}},
		{name: "When a Job appears while the selected member is still healthy, it should reject a premature Job", delayWitness: true, wantError: "preceded selected member", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call == 1 {
				return nil
			}
			return job
		}},
		{name: "When no restart or recovery Job occurs, it should stop on cancellation rather than establish recovery", cancelAt: "third-job", wantCause: context.Canceled, observed: func(pods *corev1.PodList) {
			pods.Items[0].Status.ContainerStatuses[0].RestartCount = 2
		}, job: func(call int, job *batchv1.Job) *batchv1.Job { return nil }},
		{name: "When a Job has another HC name, it should reject wrong ownership", wantError: "ownership labels", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.Labels["hypershift.openshift.io/cluster-name"] = "unrelated"
			return job
		}},
		{name: "When a Job has another HC namespace, it should reject wrong ownership", wantError: "ownership labels", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.Labels["hypershift.openshift.io/cluster-namespace"] = "unrelated"
			return job
		}},
		{name: "When a Job is not active, it should remain bounded by cancellation", wantCause: context.Canceled, cancelAt: "third-job", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			job.Status.Active = 0
			return job
		}},
		{name: "When a Job is being deleted, it should not count it as active recovery", wantCause: context.Canceled, cancelAt: "fourth-job", job: func(call int, job *batchv1.Job) *batchv1.Job {
			if call < 3 {
				return nil
			}
			stamp := metav1.Now()
			job.DeletionTimestamp = &stamp
			return job
		}},
		{name: "When baseline member listing fails, it should preserve the API error without exec", beforeExec: true, listErrorAt: 1, wantCause: sentinel},
		{name: "When witness member listing fails, it should preserve the API error", listErrorAt: 2, wantCause: sentinel},
		{name: "When baseline Job lookup fails, it should preserve the API error without exec", beforeExec: true, jobErrorAt: 1, wantCause: sentinel},
		{name: "When post stimulus Job lookup fails, it should preserve the API error", jobErrorAt: 2, wantCause: sentinel},
		{name: "When corruption exec fails, it should preserve the exec error", execError: sentinel, wantCause: sentinel},
		{name: "When corruption output reports a missing path, it should fail the actual stimulus", execOutput: "No such file or directory", wantError: "command output"},
		{name: "When context is canceled initially, it should not access APIs or exec", beforeExec: true, cancelAt: "initial", wantCause: context.Canceled},
		{name: "When context is canceled at baseline Job lookup, it should not exec", beforeExec: true, cancelAt: "baseline-job", wantCause: context.Canceled},
		{name: "When context is canceled in exec, it should stop before observation", cancelAt: "exec", wantCause: context.Canceled},
		{name: "When context is canceled while polling, it should stop the timer", cancelAt: "first-observation-job", wantCause: context.Canceled},
		{name: "When context is canceled at the active Job read, it should not report success", cancelAt: "third-job", wantCause: context.Canceled},
	}
}

func TestNativeCorruptEtcdMember(t *testing.T) {
	sentinel := errors.New("controlled API or exec error")
	for _, testCase := range nativeCorruptionTestCases(sentinel) {
		t.Run(testCase.name, func(t *testing.T) { runNativeCorruptionTestCase(t, testCase, sentinel) })
	}
	testNativeCorruptionMissingInputs(t)
}

type nativeCorruptionTestState struct {
	t         *testing.T
	testCase  nativeCorruptionTestCase
	sentinel  error
	cancel    context.CancelFunc
	pods      *corev1.PodList
	selected  corev1.Pod
	events    []string
	listCalls int
	jobCalls  int
	execCalls int
}

func newNativeCorruptionTestState(t *testing.T, testCase nativeCorruptionTestCase, sentinel error, cancel context.CancelFunc) *nativeCorruptionTestState {
	pods := &corev1.PodList{}
	for index := 0; index < 3; index++ {
		pods.Items = append(pods.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("etcd-%d", index), Namespace: "clusters-recovery", UID: types.UID(fmt.Sprintf("member-%d", index))}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "etcd", RestartCount: 2, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}})
	}
	selected := *pods.Items[0].DeepCopy()
	if testCase.baseline != nil {
		testCase.baseline(pods)
	}
	return &nativeCorruptionTestState{t: t, testCase: testCase, sentinel: sentinel, cancel: cancel, pods: pods, selected: selected}
}

func (state *nativeCorruptionTestState) list(ctx context.Context, list crclient.ObjectList, options ...crclient.ListOption) error {
	state.events = append(state.events, "members")
	state.listCalls++
	if state.listCalls == state.testCase.listErrorAt {
		return state.sentinel
	}
	applied := (&crclient.ListOptions{}).ApplyOptions(options)
	if applied.Namespace != state.selected.Namespace || applied.LabelSelector.String() != "app=etcd" {
		state.t.Fatalf("wrong member query: %+v", applied)
	}
	current := state.pods.DeepCopy()
	if state.listCalls > 1 && (!state.testCase.delayWitness || state.listCalls > 2) {
		current.Items[0].Status.ContainerStatuses[0].RestartCount++
		current.Items[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
		if state.testCase.observed != nil {
			state.testCase.observed(current)
		}
	}
	*list.(*corev1.PodList) = *current
	return nil
}

func (state *nativeCorruptionTestState) get(ctx context.Context, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
	state.events = append(state.events, "job")
	state.jobCalls++
	if key.Namespace != state.selected.Namespace || key.Name != "etcd-recovery" {
		state.t.Fatalf("wrong Job query: %v", key)
	}
	if state.jobCalls == state.testCase.jobErrorAt {
		return state.sentinel
	}
	cancelCall := map[string]int{"baseline-job": 1, "first-observation-job": 2, "third-job": 3, "fourth-job": 4}[state.testCase.cancelAt]
	if state.jobCalls == cancelCall {
		state.cancel()
	}
	stamp := time.Now().Add(-time.Minute).Truncate(time.Second)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "new-job", CreationTimestamp: metav1.NewTime(stamp), Labels: map[string]string{"hypershift.openshift.io/cluster-name": "recovery", "hypershift.openshift.io/cluster-namespace": "clusters"}}, Status: batchv1.JobStatus{Active: 1}}
	if state.testCase.job != nil {
		job = state.testCase.job(state.jobCalls, job)
	} else if state.jobCalls == 1 || (!state.testCase.sameObservation && state.jobCalls < 3) || (state.testCase.delayWitness && state.jobCalls < 4) {
		job = nil
	}
	if job == nil {
		return apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, key.Name)
	}
	*object.(*batchv1.Job) = *job
	return nil
}

func (state *nativeCorruptionTestState) exec(execCtx context.Context, command []string) (string, error) {
	state.events = append(state.events, "exec")
	state.execCalls++
	if !reflect.DeepEqual(command, []string{"/bin/sh", "-c", "rm -rf /var/lib/data/member"}) {
		state.t.Fatalf("changed corruption stimulus: %v", command)
	}
	deadline, bounded := execCtx.Deadline()
	if !bounded || time.Until(deadline) > 5*time.Minute {
		state.t.Fatal("exec lacks five minute context bound")
	}
	if state.testCase.cancelAt == "exec" {
		state.cancel()
	}
	return state.testCase.execOutput, state.testCase.execError
}

func runNativeCorruptionTestCase(t *testing.T, testCase nativeCorruptionTestCase, sentinel error) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	state := newNativeCorruptionTestState(t, testCase, sentinel, cancel)
	reader := nativeCorruptionReader{list: state.list, get: state.get}
	if testCase.cancelAt == "initial" {
		cancel()
	}
	interval := time.Millisecond
	err := nativeCorruptEtcdMember(ctx, reader, crclient.ObjectKey{Namespace: "clusters", Name: "recovery"}, state.selected, state.exec, interval)
	events := state.events
	if testCase.wantError == "" && testCase.wantCause == nil {
		if err != nil {
			t.Fatalf("unexpected error: %v; operations %v", err, events)
		}
		want := []string{"members", "job", "exec", "members", "job", "members", "job"}
		if testCase.sameObservation {
			want = want[:5]
		}
		if testCase.delayWitness {
			want = append(want, "members", "job")
		}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("operations %v, want %v", events, want)
		}
	} else {
		if err == nil || (testCase.wantError != "" && !strings.Contains(err.Error(), testCase.wantError)) {
			t.Fatalf("error %v, want %q / %v; operations %v", err, testCase.wantError, testCase.wantCause, events)
		}
		if testCase.wantCause != nil && !errors.Is(err, testCase.wantCause) {
			t.Fatalf("error %v does not preserve %v", err, testCase.wantCause)
		}
	}
	if testCase.beforeExec && state.execCalls != 0 {
		t.Fatalf("unsafe exec after precondition failure: %v", events)
	}
	if !testCase.beforeExec && state.execCalls != 1 {
		t.Fatalf("exec calls %d, want one; operations %v", state.execCalls, events)
	}
	if testCase.execError != nil || testCase.execOutput != "" || testCase.cancelAt == "exec" {
		if state.listCalls != 1 || state.jobCalls != 1 {
			t.Fatalf("observed recovery after unsuccessful exec: %v", events)
		}
	}
}

func testNativeCorruptionMissingInputs(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*corev1.Pod, *crclient.ObjectKey, *time.Duration)
	}{
		{name: "When selected UID is empty, it should reject missing identity", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { pod.UID = "" }},
		{name: "When selected name is empty, it should reject missing identity", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { pod.Name = "" }},
		{name: "When selected namespace is empty, it should reject missing identity", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { pod.Namespace = "" }},
		{name: "When cluster name is empty, it should reject missing ownership", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { cluster.Name = "" }},
		{name: "When cluster namespace is empty, it should reject missing ownership", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { cluster.Namespace = "" }},
		{name: "When interval is zero, it should reject unbounded busy polling", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { *interval = 0 }},
		{name: "When interval is negative, it should reject unbounded busy polling", mutate: func(pod *corev1.Pod, cluster *crclient.ObjectKey, interval *time.Duration) { *interval = -time.Second }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			selected := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "clusters-recovery", UID: "selected"}}
			cluster := crclient.ObjectKey{Name: "recovery", Namespace: "clusters"}
			interval := time.Second
			testCase.mutate(&selected, &cluster, &interval)
			if err := nativeCorruptEtcdMember(t.Context(), nil, cluster, selected, nil, interval); err == nil || !strings.Contains(err.Error(), "corruption requires") {
				t.Fatalf("invalid input error = %v", err)
			}
		})
	}
}

func TestNativeCorruptionMemberStatus(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		baseline  func(*corev1.PodList)
		wantError bool
	}{
		{name: "When all members have known status, it should return the selected baseline"},
		{name: "When another member has no status, it should reject unknown eligibility", baseline: func(pods *corev1.PodList) { pods.Items[2].Status.ContainerStatuses = nil }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := newNativeCorruptionTestState(t, nativeCorruptionTestCase{baseline: testCase.baseline}, nil, func() {})
			status, err := nativeCorruptionMemberStatus(t.Context(), nativeCorruptionReader{list: state.list}, state.selected)
			if (err != nil) != testCase.wantError {
				t.Fatalf("status error: %v", err)
			}
			if err == nil && (status.Name != "etcd" || status.RestartCount != 2 || status.State.Running == nil) {
				t.Fatalf("wrong selected baseline: %+v", status)
			}
		})
	}
}

func TestNativeCorruptionReadJob(t *testing.T) {
	sentinel := errors.New("Job read failed")
	for _, testCase := range []struct {
		name      string
		job       func(int, *batchv1.Job) *batchv1.Job
		errorAt   int
		wantUID   types.UID
		wantCause error
	}{
		{name: "When the Job is absent, it should return no baseline"},
		{name: "When the Job exists, it should return its actual identity", job: func(call int, job *batchv1.Job) *batchv1.Job { return job }, wantUID: "new-job"},
		{name: "When Job lookup fails, it should preserve the API error", errorAt: 1, wantCause: sentinel},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := newNativeCorruptionTestState(t, nativeCorruptionTestCase{job: testCase.job, jobErrorAt: testCase.errorAt}, sentinel, func() {})
			job, err := nativeCorruptionReadJob(t.Context(), nativeCorruptionReader{get: state.get}, state.selected.Namespace)
			if !errors.Is(err, testCase.wantCause) {
				t.Fatalf("error %v, want %v", err, testCase.wantCause)
			}
			if job != nil && job.UID != testCase.wantUID {
				t.Fatalf("Job UID %s, want %s", job.UID, testCase.wantUID)
			}
			if job == nil && testCase.wantUID != "" {
				t.Fatal("lost existing Job identity")
			}
		})
	}
}

func TestNativeCorruptionBaselineJob(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		job       *batchv1.Job
		wantUID   types.UID
		wantError bool
	}{
		{name: "When baseline Job is absent, it should retain absence"},
		{name: "When baseline Job completed, it should retain the old UID", job: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{UID: "old"}, Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}}, wantUID: "old"},
		{name: "When baseline Job failed, it should retain the old UID", job: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{UID: "old"}, Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}, wantUID: "old"},
		{name: "When baseline Job is pending, it should reject prior unattributed recovery", job: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{UID: "old"}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			uid, err := nativeCorruptionBaselineJob(testCase.job)
			if (err != nil) != testCase.wantError || uid != testCase.wantUID {
				t.Fatalf("baseline UID %q, error %v", uid, err)
			}
		})
	}
}

func TestNativeCorruptionActiveJob(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		witnessed   bool
		previousUID types.UID
		wantActive  bool
		wantError   bool
	}{
		{name: "When a distinct owned Job is active after a witnessed failure, it should accept identity regardless of older server timestamps", witnessed: true, previousUID: "old", wantActive: true},
		{name: "When the old Job is active, it should reject stale identity", witnessed: true, previousUID: "new-job", wantError: true},
		{name: "When no selected failure was witnessed, it should reject unrelated recovery", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{UID: "new-job", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second)), Labels: map[string]string{"hypershift.openshift.io/cluster-name": "recovery", "hypershift.openshift.io/cluster-namespace": "clusters"}}, Status: batchv1.JobStatus{Active: 1}}
			active, err := nativeCorruptionActiveJob(job, testCase.previousUID, crclient.ObjectKey{Name: "recovery", Namespace: "clusters"}, testCase.witnessed)
			if (err != nil) != testCase.wantError || active != testCase.wantActive {
				t.Fatalf("active %t, error %v", active, err)
			}
		})
	}
}

func TestNativeObserveCorruption(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		cancel bool
	}{
		{name: "When selected failure and a distinct active Job are observed, it should finish without further mutation"},
		{name: "When observation is canceled, it should stop before API reads", cancel: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			state := newNativeCorruptionTestState(t, nativeCorruptionTestCase{sameObservation: true}, nil, cancel)
			state.listCalls, state.jobCalls = 1, 1
			if testCase.cancel {
				cancel()
			}
			err := nativeObserveCorruption(ctx, nativeCorruptionReader{list: state.list, get: state.get}, crclient.ObjectKey{Name: "recovery", Namespace: "clusters"}, state.selected, 2, "", time.Millisecond)
			if testCase.cancel {
				if !errors.Is(err, context.Canceled) || len(state.events) != 0 {
					t.Fatalf("canceled observer: %v; operations %v", err, state.events)
				}
			} else if err != nil || !reflect.DeepEqual(state.events, []string{"members", "job"}) {
				t.Fatalf("observation: %v; operations %v", err, state.events)
			}
		})
	}
}
