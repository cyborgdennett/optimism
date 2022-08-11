package derivation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"github.com/ethereum-optimism/optimism/op-node/eth"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"io"
	"math/big"
)

type BatcherCfg struct {
	// Limit the size of txs
	MinL1TxSize uint64
	MaxL1TxSize uint64

	BatcherKey *ecdsa.PrivateKey
}

type L2Batcher struct {
	log log.Logger

	rollupCfg *rollup.Config

	syncStatusAPI SyncStatusAPI
	l2            BlocksAPI
	l1            L1TXAPI

	l1Signer types.Signer

	l2ChannelOut     *derive.ChannelOut
	l2Submitting     bool // when the channel out is being submitted, and not safe to write to without resetting
	l2BufferedBlock  eth.BlockID
	l2SubmittedBlock eth.BlockID
	l2BatcherCfg     *BatcherCfg
}

func NewL2Batcher(log log.Logger, rollupCfg *rollup.Config, batcherCfg *BatcherCfg, api SyncStatusAPI, l1 L1TXAPI, l2 BlocksAPI) *L2Batcher {
	return &L2Batcher{
		log:           log,
		rollupCfg:     rollupCfg,
		syncStatusAPI: api,
		l1:            l1,
		l2:            l2,
		l2BatcherCfg:  batcherCfg,
		l1Signer:      types.LatestSignerForChainID(rollupCfg.L1ChainID),
	}
}

var _ ActorL2Batcher = (*L2Batcher)(nil)

// add next L2 block to batch buffer
func (s *L2Batcher) actL2BatchBuffer(ctx context.Context) error {
	if s.l2Submitting { // break ongoing submitting work if necessary
		s.l2ChannelOut = nil
		s.l2Submitting = false
	}
	syncStatus, err := s.syncStatusAPI.SyncStatus(ctx)
	if err != nil {
		return err
	}
	// If we just started, start at safe-head
	if s.l2SubmittedBlock == (eth.BlockID{}) {
		s.log.Info("Starting batch-submitter work at safe-head", "safe", syncStatus.SafeL2)
		s.l2SubmittedBlock = syncStatus.SafeL2.ID()
		s.l2BufferedBlock = syncStatus.SafeL2.ID()
		s.l2ChannelOut = nil
	}
	// If it's lagging behind, catch it up.
	if s.l2SubmittedBlock.Number < syncStatus.SafeL2.Number {
		s.log.Warn("last submitted block lagged behind L2 safe head: batch submission will continue from the safe head now", "last", s.l2SubmittedBlock, "safe", syncStatus.SafeL2)
		s.l2SubmittedBlock = syncStatus.SafeL2.ID()
		s.l2BufferedBlock = syncStatus.SafeL2.ID()
		s.l2ChannelOut = nil
	}
	// Create channel if we don't have one yet
	if s.l2ChannelOut == nil {
		ch, err := derive.NewChannelOut(syncStatus.HeadL1.Time)
		if err != nil { // should always succeed
			return fmt.Errorf("failed to create channel: %w", err)
		}
		s.l2ChannelOut = ch
	}
	// Add the next unsafe block to the channel
	if s.l2BufferedBlock.Number >= syncStatus.UnsafeL2.Number {
		return nil
	}
	block, err := s.l2.BlockByNumber(ctx, big.NewInt(int64(s.l2BufferedBlock.Number+1)))
	if err != nil {
		return err
	}
	if block.ParentHash() != s.l2BufferedBlock.Hash {
		s.log.Error("detected a reorg in L2 chain vs previous submitted information, resetting to safe head now", "safe_head", syncStatus.SafeL2)
		s.l2SubmittedBlock = syncStatus.SafeL2.ID()
		s.l2BufferedBlock = syncStatus.SafeL2.ID()
		s.l2ChannelOut = nil
	}
	if err := s.l2ChannelOut.AddBlock(block); err != nil { // should always succeed
		return fmt.Errorf("failed to add block to channel: %w", err)
	}
	return nil
}

// construct batch tx from L2 buffer content, submit to L1
func (s *L2Batcher) actL2BatchSubmit(ctx context.Context) error {
	// Don't run this action if there's no data to submit
	if s.l2ChannelOut == nil || s.l2ChannelOut.ReadyBytes() == 0 {
		return InvalidActionErr
	}
	// Collect the output frame
	data := new(bytes.Buffer)
	data.WriteByte(derive.DerivationVersion0)
	// subtract one, to account for the version byte
	if err := s.l2ChannelOut.OutputFrame(data, s.l2BatcherCfg.MaxL1TxSize-1); err == io.EOF {
		s.l2Submitting = false
		// there may still be some data to submit
	} else if err != nil {
		s.l2Submitting = false
		return fmt.Errorf("failed to output channel data to frame: %w", err)
	}

	nonce, err := s.l1.PendingNonceAt(ctx, s.rollupCfg.BatchSenderAddress)
	if err != nil {
		return fmt.Errorf("failed to get batcher nonce: %v", err)
	}

	gasTipCap := big.NewInt(2 * params.GWei)
	pendingHeader, err := s.l1.HeaderByNumber(ctx, big.NewInt(-1))
	if err != nil {
		return err
	}
	gasFeeCap := new(big.Int).Add(gasTipCap, new(big.Int).Mul(pendingHeader.BaseFee, big.NewInt(2)))

	rawTx := &types.DynamicFeeTx{
		ChainID:   s.rollupCfg.L1ChainID,
		Nonce:     nonce + 1,
		To:        &s.rollupCfg.BatchInboxAddress,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Data:      data.Bytes(),
	}
	gas, err := core.IntrinsicGas(rawTx.Data, nil, false, true, true)
	if err != nil {
		return fmt.Errorf("failed to compute intrinsic gas: %w", err)
	}
	rawTx.Gas = gas

	tx, err := types.SignNewTx(s.l2BatcherCfg.BatcherKey, s.l1Signer, rawTx)
	if err != nil {
		return fmt.Errorf("failed to sign tx: %w", err)
	}

	return s.l1.SendTransaction(ctx, tx)
}
