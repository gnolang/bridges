// Package edwards25519 is Go 1.25.9's crypto/internal/fips140/edwards25519 point code (edwards25519.go) and its
// field package, copied from the toolchain because the package is internal to Go: the reference the decode vectors
// of tools/ed25519vectors (decode.vec) take Point.SetBytes's verdict and coordinates from (audit round 2, E2-ED1).
// edwards25519.go, field/fe.go and field/fe_generic.go are Go's files byte for byte but their import blocks (the
// FIPS self-check import dropped, the field package's path, crypto/subtle and field/byteorder.go for the two
// internal helper packages); field/fe_noasm.go is fe_amd64_noasm.go without its build line. This file adds the one
// accessor the generator needs. Go's license: LICENSE.
package edwards25519

import "github.com/gnolang/bridges/ethereum/bridge/tools/ed25519vectors/goedwards25519/field"

// Coordinates returns v's affine x and y as their canonical 32-byte little-endian encodings.
func (v *Point) Coordinates() (x, y []byte) {
	checkInitialized(v)
	var zInv, xx, yy field.Element
	zInv.Invert(&v.z)
	xx.Multiply(&v.x, &zInv)
	yy.Multiply(&v.y, &zInv)
	return xx.Bytes(), yy.Bytes()
}
