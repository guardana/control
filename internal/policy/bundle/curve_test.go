package bundle_test

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"math/bits"
	"slices"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
)

// This file is a second implementation of the field and the curve of Ed25519
// (RFC 8032, section 5.1), written from the curve equation, from which tests
// derive values to compare with. It shares no code with crypto/ed25519 or
// bundle.go, and it is slow and not constant time, which a test does not
// need. It is built on math/bits because the dependency rule does not admit
// math/big in this tree.

const max64 = 1<<64 - 1

// fe is an integer modulo p = 2^255 - 19 in four little-endian 64-bit limbs.
// Every function below that returns one returns it reduced below p.
type fe [4]uint64

// fieldP returns p.
func fieldP() fe { return fe{0xffffffffffffffed, max64, max64, 0x7fffffffffffffff} }

// addLimbs returns a + b modulo 2^256, and whether it wrapped.
func addLimbs(a, b [4]uint64) ([4]uint64, bool) {
	var s [4]uint64
	var carry uint64
	for i := range s {
		s[i], carry = bits.Add64(a[i], b[i], carry)
	}
	return s, carry != 0
}

// reduceOnce subtracts p from v if v is at least p.
func reduceOnce(v [4]uint64) fe {
	p := fieldP()
	var d fe
	var borrow uint64
	for i := range d {
		d[i], borrow = bits.Sub64(v[i], p[i], borrow)
	}
	if borrow != 0 {
		return v
	}
	return d
}

// feAdd returns a + b. Both are below p < 2^255, so the sum does not wrap and
// is below 2p.
func feAdd(a, b fe) fe {
	s, _ := addLimbs(a, b)
	return reduceOnce(s)
}

// feNeg returns -a, that is p - a, with the negative of 0 reduced to 0.
func feNeg(a fe) fe {
	p := fieldP()
	var d [4]uint64
	var borrow uint64
	for i := range d {
		d[i], borrow = bits.Sub64(p[i], a[i], borrow)
	}
	return reduceOnce(d)
}

func feSub(a, b fe) fe { return feAdd(a, feNeg(b)) }

// feMul returns a*b. The product takes eight limbs, and 2^256 = 2p + 38, so
// modulo p the upper four count 38 times over.
func feMul(a, b fe) fe {
	var w [8]uint64
	for i := range 4 {
		var carry uint64
		for j := range 4 {
			hi, lo := bits.Mul64(a[i], b[j])
			var c uint64
			lo, c = bits.Add64(lo, w[i+j], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			w[i+j], carry = lo, hi
		}
		w[i+4] = carry
	}
	var r [4]uint64
	var top uint64 // multiples of 2^256 above r's limbs, fewer than 40
	for i := range r {
		hi, lo := bits.Mul64(w[i+4], 38)
		var c uint64
		lo, c = bits.Add64(lo, w[i], 0)
		hi += c
		lo, c = bits.Add64(lo, top, 0)
		hi += c
		r[i], top = lo, hi
	}
	// Folding top in as 38*top can wrap once. What is left is then below
	// 38*top, so folding that wrap in as 38 cannot wrap again.
	r, wrapped := addLimbs(r, [4]uint64{38 * top})
	if wrapped {
		r, _ = addLimbs(r, [4]uint64{38})
	}
	// r < 2^256 = 2p + 38 needs at most two subtractions.
	return reduceOnce(reduceOnce(r))
}

// fePow returns a^e, e in four little-endian limbs.
func fePow(a fe, e [4]uint64) fe {
	r := fe{1}
	for i := 255; i >= 0; i-- {
		r = feMul(r, r)
		if e[i/64]>>(i%64)&1 == 1 {
			r = feMul(r, a)
		}
	}
	return r
}

// feInv returns 1/a as a^(p-2), for a other than 0.
func feInv(a fe) fe {
	return fePow(a, [4]uint64{0xffffffffffffffeb, max64, max64, 0x7fffffffffffffff})
}

// sqrtM1 returns 2^((p-1)/4), with (p-1)/4 = 2^253 - 5. 2 is not a square
// modulo p, so this squares to -1.
func sqrtM1() fe {
	return fePow(fe{2}, [4]uint64{0xfffffffffffffffb, max64, max64, 0x1fffffffffffffff})
}

// feSqrt returns a square root of a, or false if a has none. p is 5 modulo 8,
// so r = a^((p+3)/8), with (p+3)/8 = 2^252 - 2, squares to a or to -a, and in
// the second case r*sqrt(-1) squares to a.
func feSqrt(a fe) (fe, bool) {
	r := fePow(a, [4]uint64{0xfffffffffffffffe, max64, max64, 0x0fffffffffffffff})
	switch feMul(r, r) {
	case a:
		return r, true
	case feNeg(a):
		return feMul(r, sqrtM1()), true
	}
	return fe{}, false
}

// curveD returns d = -121665/121666, the constant of the curve equation.
func curveD() fe { return feMul(feNeg(fe{121665}), feInv(fe{121666})) }

// point is an affine point (x, y) of -x^2 + y^2 = 1 + d*x^2*y^2.
type point struct{ x, y fe }

func identity() point { return point{y: fe{1}} }

func onCurve(pt point, d fe) bool {
	xx, yy := feMul(pt.x, pt.x), feMul(pt.y, pt.y)
	return feSub(yy, xx) == feAdd(fe{1}, feMul(d, feMul(xx, yy)))
}

// add is the curve's addition law. -1 is a square modulo p and d is not, so
// the law is complete: it holds for doubling and for the identity too, and no
// denominator is 0.
func add(p1, p2 point, d fe) point {
	xx, yy := feMul(p1.x, p2.x), feMul(p1.y, p2.y)
	dxy := feMul(d, feMul(xx, yy))
	xDen, yDen := feAdd(fe{1}, dxy), feSub(fe{1}, dxy)
	inv := feInv(feMul(xDen, yDen))
	return point{
		x: feMul(feAdd(feMul(p1.x, p2.y), feMul(p1.y, p2.x)), feMul(yDen, inv)),
		y: feMul(feAdd(yy, xx), feMul(xDen, inv)),
	}
}

// scalarMul returns [k]pt, k in four little-endian limbs.
func scalarMul(k [4]uint64, pt point, d fe) point {
	r := identity()
	for i := 255; i >= 0; i-- {
		r = add(r, r, d)
		if k[i/64]>>(i%64)&1 == 1 {
			r = add(r, pt, d)
		}
	}
	return r
}

// order returns the order of pt if it divides 8, and 0 otherwise.
func order(pt point, d fe) int {
	for n := 1; n <= 8; n *= 2 {
		if pt == identity() {
			return n
		}
		pt = add(pt, pt, d)
	}
	return 0
}

// basePoint returns B, the point with y = 4/5. Either x will do, since B and
// -B have one order.
func basePoint(t *testing.T, d fe) point {
	t.Helper()
	y := feMul(fe{4}, feInv(fe{5}))
	yy := feMul(y, y)
	x, ok := feSqrt(feMul(feSub(yy, fe{1}), feInv(feAdd(feMul(d, yy), fe{1}))))
	if !ok {
		t.Fatalf("no x for y = 4/5: the field arithmetic is wrong")
	}
	return point{x, y}
}

// scalarLimbs reads 32 little-endian bytes as four limbs.
func scalarLimbs(b []byte) [4]uint64 {
	var k [4]uint64
	for i := range k {
		k[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	return k
}

// leBytes writes four limbs as 32 little-endian bytes.
func leBytes(v [4]uint64) []byte {
	b := make([]byte, 32)
	for i, limb := range v {
		binary.LittleEndian.PutUint64(b[8*i:], limb)
	}
	return b
}

// Every encoding the library decodes to a point of order dividing 8 is on the
// test's small-order list and is refused by VerifyBytes. The set is derived
// here from p and d alone, so an entry missing from both lists fails.
//
// The points are found level by level. 2P = O leaves the identity and
// (0, -1); 2P = (0, -1) needs y = 0 and so x^2 = -1; and 2P has y = 0 where
// x^2 = -y^2, which the curve equation turns into d*u^2 + 2u - 1 = 0 for
// u = y^2. The group has 8*L points, L an odd prime, so exactly eight points
// have order dividing 8, and checkTorsion finds eight.
func TestSmallOrderListEqualsTheDerivedSet(t *testing.T) {
	d := curveD()
	if feMul(d, fe{121666}) != feNeg(fe{121665}) {
		t.Fatalf("d*121666 is not -121665: the field arithmetic is wrong")
	}
	if i := sqrtM1(); feMul(i, i) != feNeg(fe{1}) {
		t.Fatalf("sqrt(-1) squared is not -1: the field arithmetic is wrong")
	}
	pts := torsionPoints(t, d)
	checkTorsion(t, pts, d)
	derived := torsionEncodings(pts)
	listed := smallOrderY()
	slices.Sort(listed)
	if !slices.Equal(derived, listed) {
		t.Errorf("derived, not listed: %q; listed, not derived: %q", missingFrom(listed, derived), missingFrom(derived, listed))
	}
	for _, y := range derived {
		for _, signBit := range []byte{0, 0x80} {
			key := unhex(t, y)
			key[ed25519.PublicKeySize-1] |= signBit
			err := bundle.VerifyBytes([]byte(policyBody), make([]byte, ed25519.SignatureSize), "weak", bundle.Keyring{"weak": key})
			checkOnly(t, err, bundle.ErrWeakKey, "VerifyBytes under the derived key %x", key)
		}
	}
}

// torsionPoints returns every point of order dividing 8.
func torsionPoints(t *testing.T, d fe) []point {
	t.Helper()
	one, i := fe{1}, sqrtM1()
	pts := []point{identity(), {y: feNeg(one)}, {x: i}, {x: feNeg(i)}}
	// u = (-1 +/- sqrt(1 + d))/d, and only a u that is a square has a y.
	root, ok := feSqrt(feAdd(one, d))
	if !ok {
		t.Fatalf("1 + d is not a square, so no point has order 8")
	}
	invD := feInv(d)
	for _, u := range []fe{feMul(feSub(root, one), invD), feMul(feNeg(feAdd(root, one)), invD)} {
		y, ok := feSqrt(u)
		if !ok {
			continue
		}
		x, ok := feSqrt(feNeg(u))
		if !ok {
			t.Fatalf("u is a square and -u is not, which sqrt(-1) rules out")
		}
		pts = append(pts, point{x, y}, point{feNeg(x), y}, point{x, feNeg(y)}, point{feNeg(x), feNeg(y)})
	}
	return pts
}

// checkTorsion holds the derived points to what the derivation says: eight
// distinct points on the curve, of orders 1, 2, 4, 4, 8, 8, 8 and 8.
func checkTorsion(t *testing.T, pts []point, d fe) {
	t.Helper()
	orders := map[int]int{}
	for n, pt := range pts {
		if !onCurve(pt, d) {
			t.Errorf("derived point %d is not on the curve", n)
		}
		if slices.Contains(pts[:n], pt) {
			t.Errorf("derived point %d repeats an earlier one", n)
		}
		orders[order(pt, d)]++
	}
	if want := map[int]int{1: 1, 2: 1, 4: 2, 8: 4}; !maps.Equal(orders, want) {
		t.Fatalf("orders of the derived points: %v, want %v", orders, want)
	}
}

// torsionEncodings lists, sorted and with the sign bit clear, every encoding
// the library decodes to one of pts. It reads y from the low 255 bits modulo
// p, so a y below 19 has y + p as its second encoding.
func torsionEncodings(pts []point) []string {
	var out []string
	for _, pt := range pts {
		out = append(out, hex.EncodeToString(leBytes(pt.y)))
		if pt.y[1]|pt.y[2]|pt.y[3] == 0 && pt.y[0] < 19 {
			unreduced, _ := addLimbs(pt.y, fieldP())
			out = append(out, hex.EncodeToString(leBytes(unreduced)))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// missingFrom returns the entries of want that got lacks.
func missingFrom(got, want []string) []string {
	var out []string
	for _, s := range want {
		if !slices.Contains(got, s) {
			out = append(out, s)
		}
	}
	return out
}
