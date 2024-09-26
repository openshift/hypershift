package kas

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	. "github.com/onsi/gomega"
)

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
					cel.Declarations(
						decls.NewVar("object", decls.NewMapType(decls.String, decls.Dyn)),
						decls.NewVar("oldObject", decls.NewMapType(decls.String, decls.Dyn)),
						decls.NewVar("request.userInfo.username", decls.String),
					),
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
