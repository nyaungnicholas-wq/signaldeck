package rvgrade

import "math"

func StudentTTwoSidedP(t float64, df int) float64 {
	if df < 1 || math.IsNaN(t) {
		return math.NaN()
	}
	if t == 0 {
		return 1.0
	}
	if math.IsInf(t, 0) {
		return 0.0
	}
	dfF := float64(df)
	t2 := t * t
	x := dfF / (dfF + t2)
	a := dfF / 2.0
	b := 0.5
	var ix float64
	if x > (a+1)/(a+b+2) {
		ix = 1.0 - regIncBeta(1-x, b, a)
	} else {
		ix = regIncBeta(x, a, b)
	}
	if ix < 0 {
		ix = 0
	} else if ix > 1 {
		ix = 1
	}
	return ix
}

func regIncBeta(x, a, b float64) float64 {
	if x == 0 {
		return 0
	}
	if x == 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	lnB := la + lb - lab
	factor := math.Exp(a*math.Log(x) + b*math.Log(1-x) - lnB)
	cf := betacf(a, b, x)
	return factor * cf / a
}

func betacf(a, b, x float64) float64 {
	const (
		maxIT = 20000 // the fraction needs ~sqrt(df) terms at large df
		eps   = 1e-15
		fpmin = 1e-300
	)
	qab := a + b
	qap := a + 1.0
	qam := a - 1.0
	c := 1.0
	d := 1.0 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1.0 / d
	h := d
	for m := 1; m <= maxIT; m++ {
		m2 := 2 * m
		aa := float64(m) * (b - float64(m)) * x / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1.0 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1.0 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1.0 / d
		del := d * c
		h *= del
		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + float64(m2)) * (qap + float64(m2)))
		d = 1.0 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1.0 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1.0 / d
		del = d * c
		h *= del
		if math.Abs(del-1.0) < eps {
			break
		}
	}
	return h
}
