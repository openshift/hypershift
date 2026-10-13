package util

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestSupported(t *testing.T) {
	type TestStruct struct {
		Param1 string             `param:"param1"`
		Param2 *uint              `param:"param2"`
		Param3 *resource.Quantity `param:"param3"`
		Param4 []string           `param:"param4"`
		Param5 bool               `param:"param5"`
	}
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "Valid struct with all supported types",
			input:    TestStruct{},
			expected: "param1:string,param2:uint,param3:resource.Quantity,param4:[]string,param5:bool",
		},
		{
			name:     "Empty struct",
			input:    struct{}{},
			expected: "",
		},
		{
			name: "Struct with unsupported type",
			input: struct {
				Param1 int `param:"param1"`
			}{},
			expected: "panic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					if tt.expected != "panic" {
						t.Errorf("expected no panic, but got %v", r)
					}
				}
			}()

			result := Supported(tt.input)
			if result != tt.expected && tt.expected != "panic" {
				t.Errorf("expected %s, but got %s", tt.expected, result)
			}
		})
	}
}

func TestMap(t *testing.T) {
	type TestStruct struct {
		Param1 string             `param:"param1"`
		Param2 *uint              `param:"param2"`
		Param3 *resource.Quantity `param:"param3"`
		Param4 bool               `param:"param4"`
	}
	tests := []struct {
		name      string
		flagName  string
		paramsStr string
		input     interface{}
		expected  interface{}
		err       bool
	}{
		{
			name:      "Valid parameters",
			flagName:  "test-flag",
			paramsStr: "param1:value1,param2:42,param3:100Mi,param4:true",
			input:     &TestStruct{},
			expected: &TestStruct{
				Param1: "value1",
				Param2: func() *uint { u := uint(42); return &u }(),
				Param3: func() *resource.Quantity { q := resource.MustParse("100Mi"); return &q }(),
				Param4: true,
			},
			err: false,
		},
		{
			name:      "Unknown parameter",
			flagName:  "test-flag",
			paramsStr: "param1:value1,param6:value6",
			input:     &TestStruct{},
			expected:  &TestStruct{Param1: "value1"},
			err:       true,
		},
		{
			name:      "Invalid uint parameter",
			flagName:  "test-flag",
			paramsStr: "param2:invalid",
			input:     &TestStruct{},
			expected:  &TestStruct{},
			err:       true,
		},
		{
			name:      "Invalid bool parameter",
			flagName:  "test-flag",
			paramsStr: "param5:invalid",
			input:     &TestStruct{},
			expected:  &TestStruct{},
			err:       true,
		},
		{
			name:      "Empty parameters",
			flagName:  "test-flag",
			paramsStr: "",
			input:     &TestStruct{},
			expected:  &TestStruct{},
			err:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Map(tt.flagName, tt.paramsStr, tt.input)
			if (err != nil) != tt.err {
				t.Errorf("expected error: %v, got: %v", tt.err, err)
			}
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Errorf("expected: %+v, got: %+v", tt.expected, tt.input)
			}
		})
	}
}
