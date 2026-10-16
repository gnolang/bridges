// The two helpers of crypto/internal/fips140deps/byteorder that fe.go calls, over encoding/binary: with
// crypto/subtle for crypto/internal/fips140/subtle, the copy's only change from Go 1.25.9's field package
// (besides fe_noasm.go's build line: the generic multiplication everywhere, which Go's tests hold equal to the
// assembly).

package field

import "encoding/binary"

type leByteOrder struct{}

func (leByteOrder) LEUint64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

func (leByteOrder) LEPutUint64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }

var byteorder leByteOrder
