package main

import "crypto/rand"

// ---- password ----
func genPassword8() string {
	lower := "abcdefghijklmnopqrstuvwxyz"
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digs := "0123456789"
	spec := "!@#$%^&*()-_=+[]{};:,.<>?"
	all := lower + upper + digs + spec

	// Ensure all categories present: 1 lower, 1 upper, 1 digit, 1 spec + 4 random
	var b []byte
	b = append(b, lower[randInt(len(lower))])
	b = append(b, upper[randInt(len(upper))])
	b = append(b, digs[randInt(len(digs))])
	b = append(b, spec[randInt(len(spec))])
	for len(b) < 8 {
		b = append(b, all[randInt(len(all))])
	}
	// Shuffle
	for i := len(b) - 1; i > 0; i-- {
		j := randInt(i + 1)
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func randInt(n int) int {
	if n <= 0 {
		return 0
	}
	// crypto/rand for better randomness
	x := make([]byte, 4)
	_, _ = rand.Read(x)
	v := int(uint32(x[0]) | uint32(x[1])<<8 | uint32(x[2])<<16 | uint32(x[3])<<24)
	if v < 0 {
		v = -v
	}
	return v % n
}
