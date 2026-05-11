package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/google/cel-go/cel"
)

func TestGenerateIngressCelExpression(t *testing.T) {
	tests := []struct {
		name                 string
		usernames            []string
		expectedExpression   string
		shouldPassValidation bool
		inputObjects         map[string]interface{}
	}{
		{
			name:               "When no usernames are provided it should return just the ingress CEL expression",
			usernames:          []string{},
			expectedExpression: IngressCelExpression,
		},
		{
			name:                 "When a whitelisted user modifies componentRoutes it should pass",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "system:hcco",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain":          "apps.example.com",
						"componentRoutes": []interface{}{map[string]interface{}{"name": "console"}},
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies only componentRoutes it should pass",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"componentRoutes": []interface{}{map[string]interface{}{
							"namespace": "openshift-console",
							"name":      "console",
							"hostname":  "console.custom.com",
						}},
						"type": "",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain":          "apps.example.com",
						"type":            "",
						"componentRoutes": []interface{}{},
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies only appsDomain it should pass",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain":     "apps.example.com",
						"appsDomain": "custom-apps.example.com",
						"type":       "",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies domain it should fail",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: false,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "changed.example.com",
						"type":   "",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies no fields it should pass",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies the oauth componentRoute it should fail",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: false,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
						"componentRoutes": []interface{}{
							map[string]interface{}{
								"namespace": "openshift-authentication",
								"name":      "oauth-openshift",
								"hostname":  "oauth.custom.com",
							},
						},
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain":          "apps.example.com",
						"type":            "",
						"componentRoutes": []interface{}{},
					},
				},
			},
		},
		{
			name:                 "When a non-whitelisted user modifies a non-oauth componentRoute it should pass",
			usernames:            []string{"system:hcco"},
			expectedExpression:   "request.userInfo.username in ['system:hcco'] || (" + IngressCelExpression + ")",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "regular-user",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain": "apps.example.com",
						"type":   "",
						"componentRoutes": []interface{}{
							map[string]interface{}{
								"namespace": "openshift-console",
								"name":      "console",
								"hostname":  "console.custom.com",
							},
						},
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"domain":          "apps.example.com",
						"type":            "",
						"componentRoutes": []interface{}{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := generateIngressCelExpression(tt.usernames)
			g.Expect(result).To(Equal(tt.expectedExpression))

			if len(tt.usernames) != 0 {
				env, err := cel.NewEnv(
					cel.Variable("object", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("oldObject", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("request.userInfo.username", cel.StringType),
				)

				g.Expect(err).To(BeNil())

				ast, issues := env.Compile(result)
				g.Expect(issues).To(BeNil(), "Compile errors: %v", issues)

				prog, err := env.Program(ast)
				g.Expect(err).To(BeNil(), "Program errors: %v", err)

				out, _, err := prog.Eval(tt.inputObjects)
				g.Expect(err).To(BeNil())
				g.Expect(tt.shouldPassValidation).To(BeEquivalentTo(out.Value().(bool)))
			}
		})
	}
}

func TestGenerateCelExpression(t *testing.T) {
	tests := []struct {
		name                 string
		usernames            []string
		expectedExpression   string
		shouldPassValidation bool
		inputObjects         map[string]interface{}
	}{
		{
			name:                 "single username, should pass validation, should pass",
			usernames:            []string{"user1"},
			expectedExpression:   "request.userInfo.username in ['user1'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, same spec, invalid user, should pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, different spec, valid username, should pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user3",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "wrongValue",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, different spec, invalid username, should not pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: false,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "wrongValue",
					},
				},
			},
		},
		{
			name:               "no usernames, should pass",
			usernames:          []string{},
			expectedExpression: "has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := generateCelExpression(tt.usernames)
			g.Expect(result).To(Equal(tt.expectedExpression))

			if len(tt.usernames) != 0 {
				env, err := cel.NewEnv(
					cel.Variable("object", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("oldObject", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("request.userInfo.username", cel.StringType),
				)

				g.Expect(err).To(BeNil())

				ast, issues := env.Compile(result)
				g.Expect(issues).To(BeNil(), "Compile errors: %v", issues)

				prog, err := env.Program(ast)
				g.Expect(err).To(BeNil(), "Program errors: %v", err)

				out, _, err := prog.Eval(tt.inputObjects)
				g.Expect(err).To(BeNil())
				g.Expect(tt.shouldPassValidation).To(BeEquivalentTo(out.Value().(bool)))
			}
		})
	}
}
