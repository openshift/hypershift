package ginkgo

func Describe(_ string, _ ...any) bool { return true }
func Context(_ string, _ ...any) bool  { return true }
func It(_ string, _ func()) bool       { return true }
func BeforeEach(_ func()) bool         { return true }
