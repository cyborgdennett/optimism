package derivation

import (
	"context"
	"crypto/ecdsa"
	"github.com/ethereum-optimism/optimism/op-bindings/bindings"
	"github.com/ethereum-optimism/optimism/op-bindings/predeploys"
	"github.com/ethereum-optimism/optimism/op-node/eth"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/sources"
	"github.com/ethereum-optimism/optimism/op-node/testlog"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
	"math/big"
	"math/rand"
	"os"
	"path"
	"testing"
)

var testingJWTSecret = [32]byte{123}

func writeDefaultJWT(t *testing.T) string {
	// Sadly the geth node config cannot load JWT secret from memory, it has to be a file
	jwtPath := path.Join(t.TempDir(), "jwt_secret")
	if err := os.WriteFile(jwtPath, []byte(hexutil.Encode(testingJWTSecret[:])), 0600); err != nil {
		t.Fatalf("failed to prepare jwt file for geth: %v", err)
	}
	return jwtPath
}

func precompileAlloc() core.GenesisAlloc {
	alloc := make(map[common.Address]core.GenesisAccount)
	var addr [common.AddressLength]byte
	for i := 0; i < 256; i++ {
		addr[common.AddressLength-1] = byte(i)
		alloc[addr] = core.GenesisAccount{Balance: common.Big1}
	}
	return alloc
}

func TestActors(t *testing.T) {
	jwtPath := writeDefaultJWT(t)
	rng := rand.New(rand.NewSource(1234))
	genKey := func() (*ecdsa.PrivateKey, common.Address) {
		priv, err := ecdsa.GenerateKey(crypto.S256(), rng)
		require.NoError(t, err, "must generate key for test user")
		return priv, crypto.PubkeyToAddress(priv.PublicKey)
	}
	forkRng := func() *rand.Rand {
		return rand.New(rand.NewSource(rng.Int63()))
	}

	batcherPriv, batcherAddr := genKey()
	_, proposerAddr := genKey()
	_, p2pSignerAddr := genKey()
	_, priorityFeeRecipientAddr := genKey()
	_, basefeeRecipientAddr := genKey()
	_, l1FeeAddr := genKey()

	l1Alloc := precompileAlloc()
	l2Alloc := precompileAlloc()

	genesisTimestamp := uint64(1234567890)

	log := testlog.Logger(t, log.LvlInfo)

	l1Genesis := &core.Genesis{
		Config: &params.ChainConfig{
			ChainID:             big.NewInt(901),
			HomesteadBlock:      common.Big0,
			EIP150Block:         common.Big0,
			EIP155Block:         common.Big0,
			EIP158Block:         common.Big0,
			ByzantiumBlock:      common.Big0,
			ConstantinopleBlock: common.Big0,
			PetersburgBlock:     common.Big0,
			IstanbulBlock:       common.Big0,
			BerlinBlock:         common.Big0,
			LondonBlock:         common.Big0,
		},
		Alloc:      l1Alloc,
		Difficulty: common.Big0,
		ExtraData:  nil,
		GasLimit:   5000000,
		Nonce:      0,
		Timestamp:  genesisTimestamp,
		BaseFee:    big.NewInt(7),
	}

	l1BlockTime := uint64(12)
	canonL1 := L1CanonSrc(nil)
	l1Miner := NewL1Miner(log, l1Genesis, l1BlockTime, canonL1)

	l1Cl := ethclient.NewClient(l1Miner.RPCClient())
	l1GenesisBlock, err := l1Cl.BlockByNumber(context.Background(), big.NewInt(0))
	require.NoError(t, err)
	l1GenesisID := eth.BlockID{Hash: l1GenesisBlock.Hash(), Number: l1GenesisBlock.NumberU64()}

	l2Alloc[predeploys.L1BlockAddr] = core.GenesisAccount{Code: common.FromHex(bindings.L1BlockDeployedBin), Balance: common.Big0}
	l2Alloc[predeploys.L2ToL1MessagePasserAddr] = core.GenesisAccount{Code: common.FromHex(bindings.L2ToL1MessagePasserDeployedBin), Balance: common.Big0}
	l2Alloc[predeploys.GasPriceOracleAddr] = core.GenesisAccount{Code: common.FromHex(bindings.GasPriceOracleDeployedBin), Balance: common.Big0, Storage: map[common.Hash]common.Hash{
		// storage for GasPriceOracle to have transctorPath wallet as owner
		common.BigToHash(big.NewInt(0)): common.HexToHash("0x8A0A996b22B103B500Cd0F20d62dF2Ba3364D295"),
	}}

	l2Genesis := &core.Genesis{
		Config: &params.ChainConfig{
			ChainID:                 big.NewInt(902),
			HomesteadBlock:          common.Big0,
			EIP150Block:             common.Big0,
			EIP155Block:             common.Big0,
			EIP158Block:             common.Big0,
			ByzantiumBlock:          common.Big0,
			ConstantinopleBlock:     common.Big0,
			PetersburgBlock:         common.Big0,
			IstanbulBlock:           common.Big0,
			BerlinBlock:             common.Big0,
			LondonBlock:             common.Big0,
			MergeNetsplitBlock:      common.Big0,
			TerminalTotalDifficulty: common.Big0,
			Optimism: &params.OptimismConfig{
				BaseFeeRecipient: basefeeRecipientAddr,
				L1FeeRecipient:   l1FeeAddr,
			},
		},
		Alloc:      l2Alloc,
		Difficulty: common.Big1,
		GasLimit:   5000000,
		Nonce:      0,
		// must be equal (or higher, while within bounds) as the L1 anchor point of the rollup
		Timestamp: genesisTimestamp,
		BaseFee:   big.NewInt(7),
	}
	l2Eng := NewL2Engine(log, l2Genesis, l1GenesisID, jwtPath)

	l2Cl := ethclient.NewClient(l2Eng.RPCClient())
	l2GenesisBlock, err := l2Cl.BlockByNumber(context.Background(), big.NewInt(0))
	require.NoError(t, err)
	l2GenesisID := eth.BlockID{Hash: l2GenesisBlock.Hash(), Number: l2GenesisBlock.NumberU64()}

	batcherCfg := &BatcherCfg{
		MinL1TxSize: 0,
		MaxL1TxSize: 128_000,
		BatcherKey:  batcherPriv,
	}

	rollupCfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L1:     l1GenesisID,
			L2:     l2GenesisID,
			L2Time: l2Genesis.Timestamp,
		},
		BlockTime:              2,
		MaxSequencerDrift:      10,
		SeqWindowSize:          32,
		ChannelTimeout:         40,
		L1ChainID:              l1Genesis.Config.ChainID,
		L2ChainID:              l2Genesis.Config.ChainID,
		P2PSequencerAddress:    p2pSignerAddr,
		FeeRecipientAddress:    priorityFeeRecipientAddr,
		BatchInboxAddress:      common.Address{0xff, 0x02},
		BatchSenderAddress:     batcherAddr,
		DepositContractAddress: common.Address{}, // TODO
	}

	l1Client, err := sources.NewL1Client(l1Miner.RPCClient(), log, nil, sources.L1ClientDefaultConfig(rollupCfg, false))
	require.NoError(t, err)

	engClient, err := sources.NewEngineClient(l2Eng.RPCClient(), log, nil, sources.EngineClientDefaultConfig(rollupCfg))

	l2Seq := NewL2Sequencer(log, l1Client, engClient, rollupCfg)

	batcher := NewL2Batcher(log, rollupCfg, batcherCfg, l2Seq, l1Cl, l2Cl)

	portal, err := bindings.NewOptimismPortal(rollupCfg.DepositContractAddress, l1Cl)
	require.NoError(t, err)

	userEnv := &UserEnvironment{
		l1TxAPI:       l1Miner,
		l2TxAPI:       l2Eng,
		l1ChainID:     l1Genesis.Config.ChainID,
		l2ChainID:     l2Genesis.Config.ChainID,
		l1Signer:      types.LatestSigner(l1Genesis.Config),
		l2Signer:      types.LatestSigner(l2Genesis.Config),
		bindingPortal: portal,
		addresses:     []common.Address{common.Address{}, common.Address{1}}, // TODO
	}

	userA := NewUser(log, genKey(), forkRng(), userEnv)
	userB := NewUser(log, genKey(), forkRng(), userEnv)

	actions := []Action{
		l1Miner.actL1StartBlock,
		l1Miner.actL1IncludeTx(rollupCfg.BatchSenderAddress),
		l1Miner.actL1EndBlock,
		l1Miner.actL1FinalizeNext,
		l1Miner.actL1SafeNext,
		l2Eng.actL2IncludeTx,
		l2Seq.actL2PipelineStep,
		l2Seq.actL2StartBlock,
		l2Seq.actL2EndBlock,
		userA.actL2AddTx,
		userB.actL2AddTx,
		batcher.actL2BatchBuffer,
		batcher.actL2BatchSubmit,
	}

	// fuzz, repeat, etc.
}



