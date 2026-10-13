package util

import (
	"crypto/md5"
	"fmt"
)

func ComputeHash(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
