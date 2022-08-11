package derivation

import (
	"context"
	"errors"
	"github.com/ethereum-optimism/optimism/op-node/eth"
	"github.com/ethereum-optimism/optimism/op-node/rollup/driver"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"math/big"
)

type OutputRootAPI interface {
	OutputAtBlock(ctx context.Context, number rpc.BlockNumber) ([]eth.Bytes32, error)
}

type SyncStatusAPI interface {
	SyncStatus(ctx context.Context) (*driver.SyncStatus, error)
}

type BlocksAPI interface {
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
}

type L1TXAPI interface {
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
}

var InvalidActionErr = errors.New("invalid action")

type Action func(ctx context.Context) error

type ActorL1Replica interface {
	actL1Sync(ctx context.Context) error
	actL1RewindToParent(ctx context.Context) error
	actL1RPCFail(ctx context.Context) error
}

type ActorL1Miner interface {
	actL1StartBlock(ctx context.Context) error
	actL1IncludeTx(from common.Address) Action
	actL1EndBlock(ctx context.Context) error
	actL1FinalizeNext(ctx context.Context) error
	actL1SafeNext(ctx context.Context) error
}

type ActorL2Batcher interface {
	actL2BatchBuffer(ctx context.Context) error
	actL2BatchSubmit(ctx context.Context) error
}

type ActorL2Proposer interface {
	actProposeOutputRoot(ctx context.Context) error
}

type ActorL2Engine interface {
	actL2IncludeTx(ctx context.Context) error
	actL2RPCFail(ctx context.Context) error
	// TODO snap syncing action things
}

type ActorL2Verifier interface {
	actL2PipelineStep(ctx context.Context) error
	actL2UnsafeGossipReceive(ctx context.Context) error
}

type ActorL2Sequencer interface {
	ActorL2Verifier
	actL2StartBlock(ctx context.Context) error
	actL2EndBlock(ctx context.Context) error
	actL2TryKeepL1Origin(ctx context.Context) error
	actL2UnsafeGossipFail(ctx context.Context) error
}

type ActorUser interface {
	actL1Deposit(ctx context.Context) error
	actL1AddTx(ctx context.Context) error
	actL2AddTx(ctx context.Context) error
	// TODO withdrawal tx
}

// TODO: action to sync/propagate tx pool on L1/L2 between replica and miner


