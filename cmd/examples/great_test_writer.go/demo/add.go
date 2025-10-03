package demo

import "math/big"

func Add(a, b int) int {
	return a + b
}

func SuperAdd(a, b *big.Int) *big.Int {
	return a.Add(a, b)
}

func UltraAdd[T interface{ Add(a, b T) T }](a, b T) T {
	return a.Add(a, b)
}
