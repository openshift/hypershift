package util

var (
	Version414 = "4.14"
	Version51  = "5.1"
)

type Predicate[T any] func(T) (bool, string, error)

func GetConfig() (any, error) {
	return nil, nil
}

func IsLessThan(a, b string) bool {
	return a < b
}

func AtLeast(a, b string) bool {
	return a >= b
}

func IsGreaterThanOrEqualTo(a, b string) bool {
	return a >= b
}

func SetReleaseImageVersion() error {
	return nil
}

func ShouldRunKarpenterTests() bool {
	return true
}
