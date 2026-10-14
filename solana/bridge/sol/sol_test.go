package sol

import (
	"bytes"
	"testing"
)

// The discriminators the programs hard-code (message-recipient-interface and interchain-security-module-interface
// lib.rs) are the sha256 prefixes of these names.
func TestDiscriminators(t *testing.T) {
	for _, c := range []struct {
		got  []byte
		want []byte
	}{
		{discISMAccountMetas, []byte{190, 214, 218, 129, 67, 97, 4, 76}},
		{discHandleAccountMetas, []byte{194, 141, 30, 82, 241, 41, 169, 52}},
		{discVerifyAccountMetas, []byte{200, 65, 157, 12, 89, 255, 131, 216}},
		{disc("hyperlane-message-recipient:handle"), []byte{33, 210, 5, 66, 196, 212, 239, 142}},
		{disc("hyperlane-message-recipient:interchain-security-module"), []byte{45, 18, 245, 87, 234, 46, 246, 15}},
		{disc("hyperlane-interchain-security-module:verify"), []byte{243, 53, 214, 0, 208, 18, 231, 67}},
		{disc("hyperlane-interchain-security-module:type"), []byte{105, 97, 97, 88, 63, 124, 106, 18}},
	} {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%v, want %v", c.got, c.want)
		}
	}
}
