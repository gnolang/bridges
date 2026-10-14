package sol

import (
	"net/http"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// RPCTimeout bounds every Solana RPC call a machine makes (solana-go's own default is five minutes: one stalled
// endpoint would hold a signer's or a monitor's loop for that long).
const RPCTimeout = 30 * time.Second

// NewRPC is an RPC client whose every call ends within RPCTimeout.
func NewRPC(url string) *rpc.Client {
	return rpc.NewWithCustomRPCClient(jsonrpc.NewClientWithOpts(url, &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Timeout: RPCTimeout}}))
}

// PrivateKeyFromFile reads a solana-keygen keypair file, refusing one another user could read or an empty one
// (hl.CheckKeyFile).
func PrivateKeyFromFile(path string) (solana.PrivateKey, error) {
	if err := hl.CheckKeyFile(path); err != nil {
		return nil, err
	}
	return solana.PrivateKeyFromSolanaKeygenFile(path)
}
