@AGENTS.md

## Project-Specific Guidelines

For detailed guidance on specific aspects of development, see:

- [100-go-mistakes.md](.claude/100-go-mistakes.md) - Go best practices and common pitfalls to avoid
- [code-formatting.md](.claude/code-formatting.md) - Code quality, formatting, and testing conventions
- [code-review.md](.claude/code-review.md) - Pre-submission review guidelines and checklist
- [git-commit-format.md](.claude/git-commit-format.md) - Conventional commit message format and examples
- [create-jira-items.md](.claude/create-jira-items.md) - JIRA ticket creation templates and guidelines
- [debug-aro-hcp-e2e.md](.claude/debug-aro-hcp-e2e.md) - ARO HCP e2e test debugging workflows
- this is the first task of the bigger migration project that will end up with NO usage of testing.T and full, kuberenetes/openshift style adoption of Ginkgo. Duplication now is only a transient state between migration. Now think deeply abouth the situation and propose the best path forward to achieve a full, ginkgo version of the create_cluster test, while honoring the following:
1. all existing e2e should stay the same
2. the new modified framework should exist under e2e/framework
3. migrate only the create_cluster_test
4. avoid splitting files and huge refactoring, favor surgical changes that show how testing.T was replaced with the Ginkgo approach

if you'll duplicate util functions etc, don't scaffold, duplicate and create a purely ginkgo version of those, which themselves have surgical replacement of testing.T with the proper, Ginkgo equivalant. And so on, keep going until we have a surgically sliced set of functions, packages + the one test that together fully run the equivanent of the testing.T version of that test
- this is the first task of the bigger migration project that will end up with NO usage of testing.T and full, kuberenetes/openshift style adoption of Ginkgo. Duplication now is only a transient state between migration. Now think deeply abouth the situation and propose the best path forward to achieve a full, ginkgo version of the create_cluster test, while honoring the following:
1. all existing e2e should stay the same
2. the new modified framework should exist under e2e/framework
3. migrate only the create_cluster_test
4. avoid splitting files and huge refactoring, favor surgical changes that show how testing.T was replaced with the Ginkgo approach

if you'll duplicate util functions etc, don't scaffold, duplicate and create a purely ginkgo version of those, which themselves have surgical replacement of testing.T with the proper, Ginkgo equivalant. And so on, keep going until we have a surgically sliced set of functions, packages + the one test that together fully run the equivanent of the testing.T version of that test
- when running the e2e tests, make sure you add the proper verbosity flags based on whether they're normal or ginkgo