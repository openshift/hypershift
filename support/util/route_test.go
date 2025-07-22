package util

import (
	"fmt"
	"math/rand"
	"testing"

	kvalidation "k8s.io/apimachinery/pkg/util/validation"
)

func TestShortenName(t *testing.T) {
	for i := 0; i < 10; i++ {
		shortName := randSeq(rand.Intn(kvalidation.DNS1123SubdomainMaxLength-10) + 1)
		longName := randSeq(kvalidation.DNS1123SubdomainMaxLength + rand.Intn(100))

		tests := []struct {
			base, suffix, expected string
		}{
			{
				base:     shortName,
				suffix:   "deploy",
				expected: shortName + "-deploy",
			},
			{
				base:     longName,
				suffix:   "deploy",
				expected: longName[:kvalidation.DNS1123SubdomainMaxLength-16] + "-" + hash(longName) + "-deploy",
			},
			{
				base:     shortName,
				suffix:   longName,
				expected: shortName + "-" + hash(shortName+"-"+longName),
			},
			{
				base:     "",
				suffix:   shortName,
				expected: "-" + shortName,
			},
			{
				base:     "",
				suffix:   longName,
				expected: "-" + hash("-"+longName),
			},
			{
				base:     shortName,
				suffix:   "",
				expected: shortName + "-",
			},
			{
				base:     longName,
				suffix:   "",
				expected: longName[:kvalidation.DNS1123SubdomainMaxLength-10] + "-" + hash(longName) + "-",
			},
			{
				base: "infraID-clusterName",
				suffix: "nodePoolName",
				expected: "infraID-clusterName-nodePoolName",
			},
		}

		for j, test := range tests {
			t.Run(fmt.Sprintf("test-%d-%d", i, j), func(t *testing.T) {
				result := ShortenName(test.base, test.suffix, kvalidation.DNS1123SubdomainMaxLength)
				if result != test.expected {
					t.Errorf("Got unexpected result. Expected: %s Got: %s", test.expected, result)
				}
			})
		}
	}
}

func TestShortenNameIsDifferent(t *testing.T) {
	shortName := randSeq(32)
	deployerName := ShortenName(shortName, "deploy", kvalidation.DNS1123SubdomainMaxLength)
	builderName := ShortenName(shortName, "build", kvalidation.DNS1123SubdomainMaxLength)
	if deployerName == builderName {
		t.Errorf("Expecting names to be different: %s\n", deployerName)
	}
	longName := randSeq(kvalidation.DNS1123SubdomainMaxLength + 10)
	deployerName = ShortenName(longName, "deploy", kvalidation.DNS1123SubdomainMaxLength)
	builderName = ShortenName(longName, "build", kvalidation.DNS1123SubdomainMaxLength)
	if deployerName == builderName {
		t.Errorf("Expecting names to be different: %s\n", deployerName)
	}
}

func TestShortenNameReturnShortNames(t *testing.T) {
	base := randSeq(32)
	for maxLength := 0; maxLength < len(base)+2; maxLength++ {
		for suffixLen := 0; suffixLen <= maxLength+1; suffixLen++ {
			suffix := randSeq(suffixLen)
			got := ShortenName(base, suffix, maxLength)
			if len(got) > maxLength {
				t.Fatalf("len(GetName(%[1]q, %[2]q, %[3]d)) = len(%[4]q) = %[5]d; want %[3]d", base, suffix, maxLength, got, len(got))
			}
		}
	}
}

// From k8s.io/kubernetes/pkg/api/generator.go
var letters = []rune("abcdefghijklmnopqrstuvwxyz0123456789-")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
