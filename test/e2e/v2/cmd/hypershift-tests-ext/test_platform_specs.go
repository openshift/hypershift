//go:build e2ev2

package main

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/openshift/hypershift/test/e2e/v2/internal"
)

func init() {
	if os.Getenv("HYPERSHIFT_PLATFORM") != "test" {
		return
	}

	suites = append(suites,
		e.Suite{Name: "hypershift/test", Description: "test platform e2e tests", Qualifiers: []string{`name.contains("[Feature:TestPlatform]")`}},
		e.Suite{Name: "hypershift/test/step-1", Qualifiers: []string{`labels.exists(l, l=="test-step-1")`}, Parallelism: 1},
		e.Suite{Name: "hypershift/test/step-2", Qualifiers: []string{`labels.exists(l, l=="test-step-2")`}, Parallelism: 1},
	)

	internal.RegisterEnvVarSpec(internal.EnvVarSpec{
		Name:        "TEST_PLATFORM_ENV_VALUE",
		Description: "Test platform env value resolved from SHARED_DIR.",
		SharedFile:  "test_env_value",
	})
	internal.RegisterEnvVarSpec(internal.EnvVarSpec{
		Name:           "TEST_PLATFORM_ENV_FILE",
		Description:    "Test platform env file path resolved from SHARED_DIR.",
		SharedFilePath: "test_env_file",
	})
	internal.RegisterEnvVarSpec(internal.EnvVarSpec{
		Name:        "TEST_PLATFORM_RELEASE_IMAGE",
		Description: "Test platform release image resolved from RELEASE_IMAGE_LATEST.",
		FallbackEnv: "RELEASE_IMAGE_LATEST",
	})

	Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Pool A",
		Label("test-pool-a", "taint:test-exclusive"), func() {

			It("pool-a spec 1 should pass", func() {
				time.Sleep(100 * time.Millisecond)
				Expect(true).To(BeTrue())
			})

			It("pool-a spec 2 should pass", func() {
				time.Sleep(100 * time.Millisecond)
				Expect(true).To(BeTrue())
			})
		})

	Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Pool B",
		Label("test-pool-b"), func() {

			It("pool-b spec 1 should pass", func() {
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

	Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Sequential Step 1",
		Label("test-step-1"), func() {

			It("step-1 should complete successfully", func() {
				if os.Getenv("TEST_PLATFORM_STEP1_FAIL") == "true" {
					Fail("step-1 forced failure via TEST_PLATFORM_STEP1_FAIL")
				}
				time.Sleep(100 * time.Millisecond)
				Expect(true).To(BeTrue())
			})

			It("step-1 validates platform env from SHARED_DIR files", func() {
				if expected := os.Getenv("EXPECTED_ENV_VALUE"); expected != "" {
					Expect(internal.GetEnvVarValue("TEST_PLATFORM_ENV_VALUE")).To(Equal(expected))
				}
				if os.Getenv("EXPECTED_ENV_FILE_EXISTS") == "true" {
					path := internal.GetEnvVarValue("TEST_PLATFORM_ENV_FILE")
					Expect(path).NotTo(BeEmpty(), "TEST_PLATFORM_ENV_FILE should be set")
					_, err := os.Stat(path)
					Expect(err).NotTo(HaveOccurred(), "TEST_PLATFORM_ENV_FILE should point to an existing file")
				}
			})

			It("step-1 validates release image env", func() {
				if expected := os.Getenv("EXPECTED_RELEASE_IMAGE"); expected != "" {
					Expect(internal.GetEnvVarValue("TEST_PLATFORM_RELEASE_IMAGE")).To(Equal(expected))
				}
			})
		})

	Describe("[sig-hypershift][Jira:Hypershift][Feature:TestPlatform] Test Platform Sequential Step 2",
		Label("test-step-2"), func() {

			It("step-2 should only run if step-1 passed", func() {
				time.Sleep(100 * time.Millisecond)
				Expect(true).To(BeTrue())
			})
		})
}
