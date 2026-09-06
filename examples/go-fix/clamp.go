package clamp

// Clamp restricts value to the inclusive interval [min, max].
// Callers provide min <= max.
func Clamp(value, min, max int) int {
	if value < min {
		return value
	}
	if value > max {
		return max
	}
	return value
}
