package demo

func Subtract(a, b int) int {
	// Bug: should subtract, but currently adds.
	return a + b
}
