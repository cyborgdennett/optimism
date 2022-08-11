package derivation

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"github.com/ethereum-optimism/optimism/op-bindings/bindings"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"math/big"
	"math/rand"
)

type UserEnvironment struct {
	l1TxAPI TXAPI
	l2TxAPI TXAPI

	l1ChainID *big.Int
	l2ChainID *big.Int

	l1Signer types.Signer
	l2Signer types.Signer

	// contract bindings
	bindingPortal *bindings.OptimismPortal

	// TODO add bindings/actions for interacting with the other contracts

	// This should be seeded with:
	//  - reserve 0 for selecting nil
	//  - addresses of above accounts
	//  - addresses of system contracts
	//  - precompiles
	//  - random addresses
	//  - zero address
	//  - masked L2 version of all the above
	addresses []common.Address
}

type User struct {
	log log.Logger

	rng *rand.Rand

	account *ecdsa.PrivateKey
	address common.Address


	// selectedToAddr is the address used as recipient in txs: addresses[selectedToAddr % uint64(len(s.addresses)]
	selectedToAddr uint64

	env *UserEnvironment
}

var _ ActorUser = (*User)(nil)

func NewUser(log log.Logger, priv *ecdsa.PrivateKey, rng *rand.Rand, env *UserEnvironment) *User {
	return &User{
		log: log,
		rng: rng,
		account: priv,
		address: crypto.PubkeyToAddress(priv.PublicKey),
		selectedToAddr: 0,
		env: env,
	}
}

// add rollup deposit to L1 tx queue
func (s *User) actL1Deposit(ctx context.Context) error {
	if !s.env.l1TxAPI.TxSpace() {
		return InvalidActionErr
	}

	// create a regular random tx on L1, append to L1 tx queue
	nonce, err := s.env.l1TxAPI.NonceFor(ctx, s.address)
	if err != nil {
		return fmt.Errorf("failed to get L1 nonce for account %s: %w", s.address, err)
	}

	// L2 recipient address
	toIndex := s.selectedToAddr % uint64(len(s.env.addresses))
	toAddr := s.env.addresses[toIndex]
	isCreation := toIndex == 0

	// TODO randomize deposit contents
	value := big.NewInt(1_000_000_000)
	gasLimit := uint64(50_000)
	data := []byte{0x42}

	txOpts, err := bind.NewKeyedTransactorWithChainID(s.account, s.env.l1ChainID)
	if err != nil {
		return fmt.Errorf("failed to create NewKeyedTransactorWithChainID for L1 deposit: %w", err)
	}
	txOpts.Nonce = new(big.Int).SetUint64(nonce)
	txOpts.NoSend = true
	// TODO: maybe change the txOpts L1 fee parameters

	tx, err := s.env.bindingPortal.DepositTransaction(txOpts, toAddr, value, gasLimit, isCreation, data)
	if err != nil {
		return fmt.Errorf("failed to create deposit tx: %w", err)
	}

	return s.env.l1TxAPI.ScheduleTx(ctx, tx)
}

// add regular tx to L1 tx queue
func (s *User) actL1AddTx(ctx context.Context) error {
	if !s.env.l1TxAPI.TxSpace() {
		return InvalidActionErr
	}
	// create a regular random tx on L1, append to L1 tx queue
	nonce, err := s.env.l1TxAPI.NonceFor(ctx, s.address)
	if err != nil {
		return fmt.Errorf("failed to get L1 nonce for account %s: %w", s.address, err)
	}
	toIndex := s.selectedToAddr % uint64(len(s.env.addresses))
	var to *common.Address
	if toIndex > 0 {
		to = &s.env.addresses[toIndex]
	}
	// TODO: randomize tx contents
	tx := types.MustSignNewTx(s.account, s.env.l1Signer, &types.DynamicFeeTx{
		ChainID:   s.env.l1ChainID,
		Nonce:     nonce,
		To:        to,
		Value:     big.NewInt(1_000_000_000),
		GasTipCap: big.NewInt(10),
		GasFeeCap: big.NewInt(200),
		Gas:       21000,
	})
	return s.env.l1TxAPI.ScheduleTx(ctx, tx)
}

// add regular tx to L2 tx queue
func (s *User) actL2AddTx(ctx context.Context) error {
	if !s.env.l2TxAPI.TxSpace() {
		return InvalidActionErr
	}
	// create a regular random tx on L1, append to L1 tx queue
	nonce, err := s.env.l2TxAPI.NonceFor(ctx, s.address)
	if err != nil {
		return fmt.Errorf("failed to get L1 nonce for account %s: %w", s.address, err)
	}
	toIndex := s.selectedToAddr % uint64(len(s.env.addresses))
	var to *common.Address
	if toIndex > 0 {
		to = &s.env.addresses[toIndex]
	}
	// TODO: randomize tx contents
	tx := types.MustSignNewTx(s.account, s.env.l2Signer, &types.DynamicFeeTx{
		ChainID:   s.env.l2ChainID,
		Nonce:     nonce,
		To:        to,
		Value:     big.NewInt(1_000_000_000),
		GasTipCap: big.NewInt(10),
		GasFeeCap: big.NewInt(200),
		Gas:       21000,
	})
	return s.env.l2TxAPI.ScheduleTx(ctx, tx)
}
