package observe

import "math"

func mathNaN() float64          { return math.NaN() }
func mathInf(s float64) float64 { return math.Inf(int(s)) }
