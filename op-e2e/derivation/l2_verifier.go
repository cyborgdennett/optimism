package derivation

import (
	"context"
	"errors"
	"fmt"
	"github.com/ethereum-optimism/optimism/op-node/eth"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-node/rollup/driver"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
	"io"
)

type L2Verifier struct {
	log log.Logger

	eng derive.Engine

	// L2 rollup
	derivation     *derive.DerivationPipeline
	l2PipelineIdle bool
	l2Building bool

	rollupCfg *rollup.Config
}

var _ OutputRootAPI = (*L2Verifier)(nil)

var _ SyncStatusAPI = (*L2Verifier)(nil)


func NewL2Verifier(log log.Logger, l1 derive.L1Fetcher, eng derive.Engine, cfg *rollup.Config) *L2Verifier {
	return &L2Verifier{
		log: log,
		eng: eng,
		derivation: derive.NewDerivationPipeline(log, cfg, l1, eng, TestMetrics{}),
		l2PipelineIdle: true,
		l2Building: false,
		rollupCfg: cfg,
	}
}

func (s *L2Verifier) OutputAtBlock(ctx context.Context, number rpc.BlockNumber) ([]eth.Bytes32, error) {
	return nil, fmt.Errorf("todo OutputAtBlock %w", InvalidActionErr)
}

func (s *L2Verifier) SyncStatus(ctx context.Context) (*driver.SyncStatus, error) {
	return &driver.SyncStatus{
		CurrentL1:   s.derivation.Progress().Origin,
		// TODO
		//HeadL1:      s.l1Head,
		//SafeL1:      s.l1Safe,
		//FinalizedL1: s.l1Finalized,
		UnsafeL2:    s.derivation.UnsafeL2Head(),
		SafeL2:      s.derivation.SafeL2Head(),
		FinalizedL2: s.derivation.Finalized(),
	}, nil
}

// run L2 derivation pipeline
func (s *L2Verifier) actL2PipelineStep(ctx context.Context) error {
	if s.l2Building {
		return InvalidActionErr
	}

	s.l2PipelineIdle = false
	err := s.derivation.Step(context.Background())
	if err == io.EOF {
		s.l2PipelineIdle = true
		return nil
	} else if err != nil && errors.Is(err, derive.ErrReset) {
		s.log.Warn("Derivation pipeline is reset", "err", err)
		s.derivation.Reset()
		return nil
	} else if err != nil && errors.Is(err, derive.ErrTemporary) {
		s.log.Warn("Derivation process temporary error", "err", err)
		return nil
	} else if err != nil && errors.Is(err, derive.ErrCritical) {
		return fmt.Errorf("derivation failed critically: %w", err)
	} else {
		return nil
	}
}

// process payload from gossip
func (s *L2Verifier) actL2UnsafeGossipReceive(ctx context.Context) error {
	return InvalidActionErr
}
