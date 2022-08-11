package derivation

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"math/big"
)

type L1Miner struct {
	L1Replica

	// L1 block building data
	l1BuildingHeader *types.Header             // block header that we add txs to for block building
	l1BuildingState  *state.StateDB            // state used for block building
	l1GasPool        *core.GasPool             // track gas used of ongoing building
	pendingIndices   map[common.Address]uint64 // per account, how many txs from the pool were already included in the block, since the pool is lagging behind block mining.
	l1Transactions   []*types.Transaction      // collects txs that were successfully included into current block build
	l1Receipts       []*types.Receipt          // collect receipts of ongoing building
	l1TimeDelta      uint64                    // how time to add to next block timestamp. Minimum of 1.
	l1Building       bool
	l1TxFailed       []*types.Transaction // log of failed transactions which could not be included
}

var _ ActorL1Miner = (*L1Miner)(nil)

func NewL1Miner(log log.Logger, genesis *core.Genesis, l1BlockTime uint64, canonL1 L1CanonSrc) *L1Miner {
	rep := NewL1Replica(log, genesis, canonL1)
	return &L1Miner{
		L1Replica:   *rep,
		l1TimeDelta: l1BlockTime,
	}
}

// start new L1 block on top of head
func (s *L1Miner) actL1StartBlock(ctx context.Context) error {
	if s.l1Building {
		// not valid if we already started building a block
		return InvalidActionErr
	}
	if s.l1TimeDelta == 0 || s.l1TimeDelta > 60*60*24 {
		return fmt.Errorf("invalid time delta: %d", s.l1TimeDelta)
	}

	parent := s.l1Chain.CurrentHeader()
	parentHash := parent.Hash()
	statedb, err := state.New(parent.Root, state.NewDatabase(s.l1Database), nil)
	if err != nil {
		return fmt.Errorf("failed to init state db around block %s (state %s): %w", parentHash, parent.Root, err)
	}
	header := &types.Header{
		ParentHash: parentHash,
		Coinbase:   parent.Coinbase,
		Difficulty: common.Big0,
		Number:     new(big.Int).Add(parent.Number, common.Big1),
		GasLimit:   parent.GasLimit,
		Time:       parent.Time + s.l1TimeDelta,
		Extra:      []byte("L1 was here"),
		MixDigest:  common.Hash{}, // TODO: maybe randomize this (prev-randao value)
	}
	if s.l1Cfg.Config.IsLondon(header.Number) {
		header.BaseFee = misc.CalcBaseFee(s.l1Cfg.Config, parent)
		// At the transition, double the gas limit so the gas target is equal to the old gas limit.
		if !s.l1Cfg.Config.IsLondon(parent.Number) {
			header.GasLimit = parent.GasLimit * params.ElasticityMultiplier
		}
	}

	s.l1Building = true
	s.l1BuildingHeader = header
	s.l1BuildingState = statedb
	s.l1Receipts = make([]*types.Receipt, 0)
	s.l1Transactions = make([]*types.Transaction, 0)
	s.pendingIndices = make(map[common.Address]uint64)

	s.l1GasPool = new(core.GasPool).AddGas(header.GasLimit)
	return nil
}

// include next tx from L1 tx pool from given account
func (s *L1Miner) actL1IncludeTx(from common.Address) Action {
	return func(ctx context.Context) error {
		if !s.l1Building {
			return InvalidActionErr
		}
		i := s.pendingIndices[from]
		txs, q := s.eth.TxPool().ContentFrom(from)
		if uint64(len(txs)) >= i {
			return fmt.Errorf("no pending txs from %s, and have %d unprocessable queued txs from this account", from, len(q))
		}
		tx := txs[i]
		if tx.Gas() > s.l1BuildingHeader.GasLimit {
			return fmt.Errorf("tx consumes %d gas, more than available in L1 block %d", tx.Gas(), s.l1BuildingHeader.GasLimit)
		}
		if uint64(*s.l1GasPool) < tx.Gas() {
			return InvalidActionErr // insufficient gas to include the tx
		}
		s.pendingIndices[from] = i + 1 // won't retry the tx
		receipt, err := core.ApplyTransaction(s.l1Cfg.Config, s.l1Chain, &s.l1BuildingHeader.Coinbase,
			s.l1GasPool, s.l1BuildingState, s.l1BuildingHeader, tx, &s.l1BuildingHeader.GasUsed, *s.l1Chain.GetVMConfig())
		if err != nil {
			s.l1TxFailed = append(s.l1TxFailed, tx)
			return fmt.Errorf("failed to apply transaction to L1 block (tx %d): %w", len(s.l1Transactions), err)
		}
		s.l1Receipts = append(s.l1Receipts, receipt)
		s.l1Transactions = append(s.l1Transactions, tx)
		return nil
	}
}

// finish new L1 block, apply to chain as unsafe block
func (s *L1Miner) actL1EndBlock(ctx context.Context) error {
	if !s.l1Building {
		// not valid if we are not building a block currently
		return InvalidActionErr
	}

	s.l1Building = false
	s.l1BuildingHeader.GasUsed = s.l1BuildingHeader.GasLimit - uint64(*s.l1GasPool)
	s.l1BuildingHeader.Root = s.l1BuildingState.IntermediateRoot(s.l1Cfg.Config.IsEIP158(s.l1BuildingHeader.Number))
	block := types.NewBlock(s.l1BuildingHeader, s.l1Transactions, nil, s.l1Receipts, trie.NewStackTrie(nil))

	// Write state changes to db
	root, err := s.l1BuildingState.Commit(s.l1Cfg.Config.IsEIP158(s.l1BuildingHeader.Number))
	if err != nil {
		return fmt.Errorf("l1 state write error: %v", err)
	}
	if err := s.l1BuildingState.Database().TrieDB().Commit(root, false, nil); err != nil {
		return fmt.Errorf("l1 trie write error: %v", err)
	}

	_, err = s.l1Chain.InsertChain(types.Blocks{block})
	if err != nil {
		return fmt.Errorf("failed to insert block into l1 chain")
	}
	return nil
}

func (s *L1Miner) actL1FinalizeNext(ctx context.Context) error {
	safe := s.l1Chain.CurrentSafeBlock()
	finalizedNum := s.l1Chain.CurrentFinalizedBlock().NumberU64()
	if safe.NumberU64() <= finalizedNum {
		return InvalidActionErr // need to move forward safe block before moving finalized block
	}
	next := s.l1Chain.GetBlockByNumber(finalizedNum + 1)
	if next == nil {
		return fmt.Errorf("expected next block after finalized L1 block %d, safe head is ahead", finalizedNum)
	}
	s.l1Chain.SetFinalized(next)
	return nil
}

func (s *L1Miner) actL1SafeNext(ctx context.Context) error {
	safe := s.l1Chain.CurrentSafeBlock()
	next := s.l1Chain.GetBlockByNumber(safe.NumberU64() + 1)
	if next == nil {
		return InvalidActionErr // if head of chain is marked as safe then there's no next block
	}
	s.l1Chain.SetSafe(next)
	return nil
}
