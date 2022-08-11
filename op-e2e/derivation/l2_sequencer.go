package derivation

import (
	"context"
	"github.com/ethereum-optimism/optimism/op-node/eth"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/log"
)

type L2Sequencer struct {
	L2Verifier

	seqOldOrigin bool // stay on current L1 origin when sequencing a block, unless forced to adopt the next origin

	l1Chain derive.L1Fetcher

	payloadID eth.PayloadID

	failL2GossipUnsafeBlock error // mock error
}

var _ ActorL2Sequencer = (*L2Sequencer)(nil)

func NewL2Sequencer(log log.Logger, l1 derive.L1Fetcher, eng derive.Engine, cfg *rollup.Config) *L2Sequencer {
	ver := NewL2Verifier(log, l1, eng, cfg)
	return &L2Sequencer{
		L2Verifier:              *ver,
		seqOldOrigin:            false,
		l1Chain:                 l1,
		failL2GossipUnsafeBlock: nil,
	}
}

// start new L2 block on top of head
func (s *L2Sequencer) actL2StartBlock(ctx context.Context) error {
	if !s.l2PipelineIdle {
		return InvalidActionErr
	}
	if s.l2Building {
		// if already started
		return InvalidActionErr
	}

	parent := s.derivation.UnsafeL2Head()
	l2Timestamp := parent.Time + s.rollupCfg.BlockTime

	currentOrigin, err := s.l1Chain.L1BlockRefByHash(ctx, parent.L1Origin.Hash)
	if err != nil {
		return err
	}

	// findL1Origin test equivalent
	nextOrigin, err := s.l1Chain.L1BlockRefByNumber(ctx, currentOrigin.Number+1)
	if err == ethereum.NotFound {
		err = nil
	} else if err != nil {
		return err
	}
	origin := currentOrigin
	// if we have a next block, and are either forced to adopt it, or just don't want to stay on the old origin, then adopt it.
	if nextOrigin != (eth.L1BlockRef{}) && (l2Timestamp >= nextOrigin.Time || !s.seqOldOrigin) {
		origin = nextOrigin
	}
	s.seqOldOrigin = false

	attr, err := derive.PreparePayloadAttributes(ctx, s.rollupCfg, s.l1Chain, parent, l2Timestamp, origin.ID())
	if err != nil {
		return nil
	}
	// sequencer may not include anything extra if we run out of drift
	attr.NoTxPool = l2Timestamp >= origin.Time+s.rollupCfg.MaxSequencerDrift

	fc := eth.ForkchoiceState{
		HeadBlockHash:      s.derivation.UnsafeL2Head().Hash,
		SafeBlockHash:      s.derivation.SafeL2Head().Hash,
		FinalizedBlockHash: s.derivation.Finalized().Hash,
	}
	id, errTyp, err := derive.StartPayload(ctx, s.log, s.eng, fc, attr)
	if err != nil {
		if errTyp == derive.BlockInsertTemporaryErr {
			s.log.Warn("temporary block insertion err", "err", err)
			return nil
		}
		return err
	}
	s.l2Building = true
	s.payloadID = id

	return nil
}

// finish new L2 block, apply to chain as unsafe block
func (s *L2Sequencer) actL2EndBlock(ctx context.Context) error {
	if !s.l2Building {
		return InvalidActionErr
	}
	s.l2Building = false
	fc := eth.ForkchoiceState{
		HeadBlockHash:      s.derivation.UnsafeL2Head().Hash,
		SafeBlockHash:      s.derivation.SafeL2Head().Hash,
		FinalizedBlockHash: s.derivation.Finalized().Hash,
	}
	out, errTyp, err := derive.ConfirmPayload(ctx, s.log, s.eng, fc, s.payloadID, false)
	if err != nil {
		if errTyp == derive.BlockInsertTemporaryErr {
			s.log.Warn("temporary block insertion err", "err", err)
			return nil
		}
		return err
	}
	ref, err := derive.PayloadToBlockRef(out, &s.rollupCfg.Genesis)
	if err != nil {
		return err
	}
	s.derivation.SetUnsafeHead(ref)
	// TODO: action-test publishing of payload on p2p
	return nil
}

// attempt to keep current L1 origin, even if next origin is available
func (s *L2Sequencer) actL2TryKeepL1Origin(ctx context.Context) error {
	if s.seqOldOrigin { // don't do this twice
		return InvalidActionErr
	}
	s.seqOldOrigin = true
	return nil
}

// make next gossip receive fail
func (s *L2Sequencer) actL2UnsafeGossipFail(ctx context.Context) error {
	return InvalidActionErr
}
