package e2eutilallowlist

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "e2eutilallowlist",
	Doc:  "restricts test/e2e/v2 files to a allowlist of approved symbols from test/e2e/util",
	Run:  run,
}

// allowlist maps package path to set of allowed symbol names.
var allowlist = map[string]map[string]bool{
	"github.com/openshift/hypershift/test/e2e/util": {
		// Condition/predicate helpers
		"Condition":                    true,
		"ConditionPredicate":           true,
		"Conditions":                   true,
		"EventuallyNotFound":           true,
		"EventuallyObject":             true,
		"EventuallyObjects":            true,
		"Matches":                      true,
		"MatchesLeaderElectionFailure": true,
		"OSImageStreamPredicate":       true,
		"Predicate":                    true,
		"Reason":                       true,
		"Status":                       true,
		"String":                       true,
		"Type":                         true,
		"WithClientOptions":            true,
		"WithInterval":                 true,
		"WithTimeout":                  true,

		// Client helpers
		"GetClient":    true,
		"GetConfig":    true,
		"UpdateObject": true,

		// Wait/rollout helpers
		"WaitForControlPlaneComponentRollout":             true,
		"WaitForControlPlaneRollout":                      true,
		"WaitForDataPlaneRollout":                         true,
		"WaitForGuestKubeConfig":                          true,
		"WaitForNReadyNodesWithOptions":                   true,
		"WaitForNodePoolConfigUpdateCompleteWithPlatform": true,
		"WaitForReadyNodesByNodePool":                     true,

		// Cloud provider helpers
		"GetDefaultSecurityGroup": true,
		"PutRolePolicy":           true,

		// Utility helpers
		"ExtractVersionFromReleaseImage": true,
		"GenerateName":                   true,
		"HasFieldInCRDSchema":            true,
		"RunCommandInPod":                true,
		"SimpleNameGenerator":            true,

		// Validation helpers
		"ValidateAzureWorkloadIdentityWebhookMutation":     true,
		"ValidateIngressOperatorConfiguration":             true,
		"ValidateKubeAPIServerAllowedCIDRs":                true,
		"ValidateOAuthWithIdentityProviderViaLoadBalancer": true,

		// OIDC helpers
		"CliClientID":              true,
		"ConsoleClientID":          true,
		"ConsoleClientSecretName":  true,
		"ConsoleClientSecretValue": true,
		"ExtOIDCConfig":            true,
		"ExternalOIDCProvider":     true,
		"GetAuthenticationConfig":  true,
		"GroupPrefix":              true,
		"IssuerCAConfigmapName":    true,
		"IssuerURL":                true,
		"OIDCProviderName":         true,
		"ProviderKeycloak":         true,
		"TestUsers":                true,
		"UserPrefix":               true,
	},
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}

			// Skip blank identifiers
			if ident.Name == "_" {
				return true
			}

			// Look up the resolved object for this identifier
			obj := pass.TypesInfo.Uses[ident]
			if obj == nil {
				return true
			}

			// Skip non-package-level objects
			if obj.Pkg() == nil {
				return true
			}

			pkgPath := obj.Pkg().Path()
			const utilPkgPath = "github.com/openshift/hypershift/test/e2e/util"
			if pkgPath != utilPkgPath && !strings.HasPrefix(pkgPath, utilPkgPath+"/") {
				return true
			}

			// Check if this symbol is in the allowlist
			if isAllowed(pkgPath, obj.Name()) {
				return true
			}

			// Report diagnostic
			pass.Report(analysis.Diagnostic{
				Pos: ident.Pos(),
				End: ident.End(),
				Message: fmt.Sprintf(
					"reference to %s.%s is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency",
					pkgPath,
					obj.Name(),
				),
			})
			return true
		})
	}
	return nil, nil
}

// isAllowed checks whether a symbol is in the allowlist for its package.
func isAllowed(pkgPath string, symbolName string) bool {
	allowed, ok := allowlist[pkgPath]
	if !ok {
		return false
	}

	// Explicit match
	if allowed[symbolName] {
		return true
	}

	// Allow Version* prefix for future version constants
	if pkgPath == "github.com/openshift/hypershift/test/e2e/util" &&
		strings.HasPrefix(symbolName, "Version") {
		return true
	}

	return false
}
