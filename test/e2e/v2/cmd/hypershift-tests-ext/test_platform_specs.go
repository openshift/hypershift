//go:build e2ev2

package main

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Pool A",
	Label("test-pool-a"), func() {

		It("pool-a spec 1 should pass", func() {
			if expected := os.Getenv("EXPECTED_CLUSTER_NAME_POOL_A"); expected != "" {
				Expect(os.Getenv("E2E_HOSTED_CLUSTER_NAME")).To(Equal(expected))
			}
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})

		It("pool-a spec 2 should pass", func() {
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})
	})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Pool B",
	Label("test-pool-b"), func() {

		It("pool-b spec 1 should pass", func() {
			if expected := os.Getenv("EXPECTED_CLUSTER_NAME_POOL_B"); expected != "" {
				Expect(os.Getenv("E2E_HOSTED_CLUSTER_NAME")).To(Equal(expected))
			}
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})

		It("pool-b spec 2 should pass", func() {
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})

		It("pool-b skipped spec should be reported correctly", func() {
			Skip("intentional skip for OTE pipeline validation")
		})

		It("pool-b informing spec should not block the suite", g.Informing(), func() {
			Fail("intentional informing failure in pool-b")
		})
	})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Sequential Step 1",
	Label("test-step-1"), func() {

		It("step-1 should complete successfully", func() {
			if os.Getenv("TEST_PLATFORM_STEP1_FAIL") == "true" {
				Fail("step-1 forced failure via TEST_PLATFORM_STEP1_FAIL")
			}
			if expected := os.Getenv("EXPECTED_CLUSTER_NAME_SEQ"); expected != "" {
				Expect(os.Getenv("E2E_HOSTED_CLUSTER_NAME")).To(Equal(expected))
			}
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})
	})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Sequential Step 2",
	Label("test-step-2"), func() {

		It("step-2 should only run if step-1 passed", func() {
			if expected := os.Getenv("EXPECTED_CLUSTER_NAME_SEQ"); expected != "" {
				Expect(os.Getenv("E2E_HOSTED_CLUSTER_NAME")).To(Equal(expected))
			}
			time.Sleep(100 * time.Millisecond)
			Expect(true).To(BeTrue())
		})
	})
