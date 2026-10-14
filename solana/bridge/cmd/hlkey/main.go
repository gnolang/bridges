// Command hlkey makes a signer's key file, or prints what the chain needs from one: its ed25519 key for the gno
// mailbox's signer set.
//
//	hlkey new FILE      write a new key (mode 0600); refuses to overwrite
//	hlkey show FILE...  print the ed25519 key per file
//	hlkey gnopub FILE   print the key as gno.land writes a consensus public key (gpub…, for r/gnops/valopers)
//	hlkey pkg PATH      print a gno package path's address, and as the 32 bytes Hyperlane carries
//	hlkey sol FILE      write a new random Solana keypair (the solana-keygen JSON form, mode 0600); refuses to overwrite;
//	                    prints its public key — for machines with no Solana CLI (a key is made where it lives)
//	hlkey solpub FILE   print a Solana keypair file's public key
package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

const usage = "usage: hlkey new FILE | hlkey show FILE... | hlkey gnopub FILE | hlkey pkg PATH | hlkey sol FILE | hlkey solpub FILE"

func main() {
	if len(os.Args) < 3 {
		log.Fatal(usage)
	}
	switch os.Args[1] {
	case "new":
		if _, err := os.Stat(os.Args[2]); err == nil {
			log.Fatalf("%s exists", os.Args[2])
		}
		k, err := hl.NewValidatorKey()
		if err != nil {
			log.Fatal(err)
		}
		if err := k.Save(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		fmt.Println(hex.EncodeToString(k.EdPublic()))
	case "show":
		for _, f := range os.Args[2:] {
			k, err := hl.LoadValidatorKey(f)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(hex.EncodeToString(k.EdPublic()))
		}
	case "gnopub":
		k, err := hl.LoadValidatorKey(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(hl.GnoPubKeyBech32(k.EdPublic()))
	case "pkg":
		a := hl.GnoPackageAddress(os.Args[2])
		b, _ := hl.GnoAddressToBytes32(a)
		fmt.Printf("%s 0x%s\n", a, hex.EncodeToString(b[:]))
	case "sol":
		pub, err := newSolanaKeyFile(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(pub)
	case "solpub":
		pub, err := solanaKeyFilePub(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(pub)
	default:
		log.Fatal(usage)
	}
}
