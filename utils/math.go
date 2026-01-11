package utils

import "math"

func CantorPair(a, b uint64) uint64 {
	w := a + b
	return w*(w+1)/2 + b
}

func CantorUnpair(z uint64) (a, b uint64) {
	w := uint64((math.Sqrt(float64(8*z+1)) - 1) / 2)
	t := w * (w + 1) / 2
	b = z - t
	a = w - b
	return
}

func CantorSolveB(z, a uint64) (b uint64, ok bool) {
	w := uint64((math.Sqrt(float64(8*z+1)) - 1) / 2)
	t := w * (w + 1) / 2
	b = z - t

	if a+b != w {
		return 0, false
	}
	return b, true
}

func CantorSolveA(z, b uint64) (a uint64, ok bool) {
	w := uint64((math.Sqrt(float64(8*z+1)) - 1) / 2)
	t := w * (w + 1) / 2

	if b > z-t {
		return 0, false
	}
	a = w - b
	return a, true
}
