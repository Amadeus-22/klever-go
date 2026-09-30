package bootstrap

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/process"
	cryptoMock "github.com/klever-io/klever-go/crypto/mock"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/network/p2p"
	"github.com/klever-io/klever-go/network/p2p/libp2p"
	"github.com/klever-io/klever-go/tools/check"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEpochStartMetaSyncer_ShouldWork(t *testing.T) {
	t.Parallel()

	args := getEpochStartSyncerArgs()
	ess, err := NewEpochStartMetaSyncer(args)
	require.NoError(t, err)
	require.False(t, check.IfNil(ess))
}

func TestEpochStartMetaSyncer_SyncEpochStartMetaRegisterMessengerProcessorFailsShouldErr(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("expected error")

	args := getEpochStartSyncerArgs()
	messenger := &mock.MessengerStub{
		RegisterMessageProcessorCalled: func(_ string, _ p2p.MessageProcessor) error {
			return expectedErr
		},
	}
	args.Messenger = messenger
	ess, _ := NewEpochStartMetaSyncer(args)

	mb, err := ess.SyncEpochStartMeta(time.Second)
	require.Equal(t, expectedErr, err)
	require.Nil(t, mb)
}

func TestEpochStartMetaSyncer_SyncEpochStartMetaProcessorFailsShouldErr(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("expected error")

	args := getEpochStartSyncerArgs()
	messenger := &mock.MessengerStub{
		ConnectedPeersCalled: func() []core.PeerID {
			return []core.PeerID{"peer_0", "peer_1", "peer_2", "peer_3", "peer_4", "peer_5"}
		},
	}
	args.Messenger = messenger
	ess, _ := NewEpochStartMetaSyncer(args)

	mbIntercProc := &mock.BlockInterceptorProcessorStub{
		GetEpochStartBlockCalled: func() (*block.Block, error) {
			return nil, expectedErr
		},
	}
	ess.SetEpochStartBlockInterceptorProcessor(mbIntercProc)

	mb, err := ess.SyncEpochStartMeta(time.Second)
	require.Equal(t, expectedErr, err)
	require.Nil(t, mb)
}

func TestEpochStartMetaSyncer_SyncEpochStartMetaShouldWork(t *testing.T) {
	t.Parallel()

	expectedMb := &block.Block{Header: &block.BlockHeader{Nonce: 37}}

	args := getEpochStartSyncerArgs()
	messenger := &mock.MessengerStub{
		ConnectedPeersCalled: func() []core.PeerID {
			return []core.PeerID{"peer_0", "peer_1", "peer_2", "peer_3", "peer_4", "peer_5"}
		},
	}
	args.Messenger = messenger
	ess, err := NewEpochStartMetaSyncer(args)
	require.Nil(t, err)

	mbIntercProc := &mock.BlockInterceptorProcessorStub{
		GetEpochStartBlockCalled: func() (*block.Block, error) {
			return expectedMb, nil
		},
	}
	ess.SetEpochStartBlockInterceptorProcessor(mbIntercProc)

	mb, err := ess.SyncEpochStartMeta(time.Second)
	require.NoError(t, err)
	require.Equal(t, expectedMb, mb)
}

func getEpochStartSyncerArgs() ArgsNewEpochStartMetaSyncer {
	return ArgsNewEpochStartMetaSyncer{
		RequestHandler:          &mock.RequestHandlerStub{},
		Messenger:               &mock.MessengerStub{},
		Marshalizer:             &mock.MarshalizerMock{},
		TxSignMarshalizer:       &mock.MarshalizerMock{},
		KeyGen:                  &cryptoMock.KeyGenMock{},
		BlockKeyGen:             &cryptoMock.KeyGenMock{},
		Hasher:                  &mock.HasherMock{},
		Signer:                  &cryptoMock.SingleSignerStub{},
		BlockSigner:             &cryptoMock.SingleSignerStub{},
		ChainID:                 []byte("chain-ID"),
		WhitelistHandler:        &mock.WhiteListHandlerStub{},
		AddressPubkeyConv:       cryptoMock.NewPubkeyConverterMock(32),
		NonceConverter:          &mock.Uint64ByteSliceConverterMock{},
		HeaderIntegrityVerifier: &mock.HeaderIntegrityVerifierStub{},
		BlockProcessor:          &mock.BlockInterceptorProcessorStub{},
		ForkController:          &mock.ForkControllerStub{},
	}
}

func createMockNetMessenger(t *testing.T, netw mocknet.Mocknet) p2p.Messenger {
	args := libp2p.ArgsNetworkMessenger{
		Marshalizer:   &mock.ProtoMarshalizerMock{},
		ListenAddress: libp2p.ListenLocalhostAddrWithIp4AndTcp,
		P2pConfig: config.P2PConfig{
			Node:                config.NodeConfig{Port: "0"},
			KadDhtPeerDiscovery: config.KadDhtPeerDiscoveryConfig{Enabled: false},
			Sharding:            config.ShardingConfig{Type: p2p.NilListSharder},
		},
		SyncTimer: &libp2p.LocalSyncTimer{},
	}
	messenger, err := libp2p.NewMockMessenger(args, netw)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = messenger.Close()
	})

	return messenger
}

func linkMockNetPeers(t *testing.T, netw mocknet.Mocknet, a p2p.Messenger, b p2p.Messenger) {
	_, err := netw.LinkPeers(peer.ID(a.ID()), peer.ID(b.ID()))
	require.NoError(t, err)
	_, err = netw.ConnectPeers(peer.ID(a.ID()), peer.ID(b.ID()))
	require.NoError(t, err)
}

func createEpochStartBlockForGossip(epoch uint32) *block.Block {
	return &block.Block{
		PubKeysBitmap:     []byte{1},
		Signature:         []byte("sig"),
		ProducerSignature: []byte("psig"),
		Header: &block.BlockHeader{
			IsEpochStart:       true,
			Epoch:              epoch,
			Slot:               123456789,
			Nonce:              123456789,
			ParentHash:         []byte("parent"),
			RandSeed:           []byte("rs"),
			PrevRandSeed:       []byte("prs"),
			ChainID:            []byte("chain-ID"),
			TrieRoot:           []byte("state-root"),
			KAppsTrieRoot:      []byte("kapps-root"),
			ValidatorsTrieRoot: []byte("validators-root"),
		},
	}
}

const forgedGossipEpoch = 7777

// forgedSavesCounter counts the forged metablocks that reach the processor, so a test that
// expects them to be rejected can tell they were delivered at all
type forgedSavesCounter struct {
	*epochStartBlockProcessor
	forgedSaves atomic.Int32
}

func (fsc *forgedSavesCounter) Save(data process.InterceptedData, fromConnectedPeer core.PeerID, topic string) error {
	interceptedHdr, ok := data.(process.HdrValidatorHandler)
	if ok && interceptedHdr.HeaderHandler().GetEpoch() == forgedGossipEpoch {
		fsc.forgedSaves.Add(1)
	}

	return fsc.epochStartBlockProcessor.Save(data, fromConnectedPeer, topic)
}

// runEpochStartGossipScenario connects the syncing node directly to numHonest peers that
// gossip the honest epoch start block, to numSilent peers that never answer, and to a single
// relay. numSybils further identities connect only to the relay and gossip a forged block
// through it, so each copy reaches the syncing node from an originator it is not connected to.
func runEpochStartGossipScenario(t *testing.T, numHonest int, numSilent int, numSybils int, timeToWait time.Duration) (*block.Block, int32, error) {
	netw := mocknet.New()
	syncing := createMockNetMessenger(t, netw)
	relay := createMockNetMessenger(t, netw)
	linkMockNetPeers(t, netw, syncing, relay)

	createPeers := func(numPeers int, linkTo p2p.Messenger) []p2p.Messenger {
		peers := make([]p2p.Messenger, 0, numPeers)
		for i := 0; i < numPeers; i++ {
			messenger := createMockNetMessenger(t, netw)
			linkMockNetPeers(t, netw, messenger, linkTo)
			require.NoError(t, messenger.CreateTopic(common.BlocksTopic, true))
			peers = append(peers, messenger)
		}

		return peers
	}
	honest := createPeers(numHonest, syncing)
	_ = createPeers(numSilent, syncing)
	sybils := createPeers(numSybils, relay)
	require.NoError(t, relay.CreateTopic(common.BlocksTopic, true))

	marshalizer := &mock.ProtoMarshalizerMock{}
	processor, err := NewEpochStartBlockProcessor(
		syncing,
		&mock.RequestHandlerStub{},
		marshalizer,
		&mock.HasherMock{},
		thresholdForConsideringMetaBlockCorrect,
		2,
		2,
	)
	require.NoError(t, err)

	args := getEpochStartSyncerArgs()
	args.Messenger = syncing
	args.Marshalizer = marshalizer
	counter := &forgedSavesCounter{epochStartBlockProcessor: processor}
	args.BlockProcessor = counter
	args.ForkController = mock.NewForkControllerStub()
	syncer, err := NewEpochStartMetaSyncer(args)
	require.NoError(t, err)

	honestBuff, err := marshalizer.Marshal(createEpochStartBlockForGossip(10))
	require.NoError(t, err)
	forgedBuff, err := marshalizer.Marshal(createEpochStartBlockForGossip(forgedGossipEpoch))
	require.NoError(t, err)

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(300 * time.Millisecond):
				for _, messenger := range honest {
					messenger.Broadcast(common.BlocksTopic, honestBuff)
				}
				for _, messenger := range sybils {
					messenger.Broadcast(common.BlocksTopic, forgedBuff)
				}
			}
		}
	}()

	mb, err := syncer.SyncEpochStartMeta(timeToWait)

	return mb, counter.forgedSaves.Load(), err
}

func TestEpochStartMetaSyncer_ManyOriginatorsThroughOneConnectedPeerShouldNotReachConsensus(t *testing.T) {
	t.Parallel()

	// 10 connected peers give a target of 7; the 7 relayed originators are not connected, so
	// they hold no vote
	mb, forgedSaves, err := runEpochStartGossipScenario(t, 0, 9, 7, 3*time.Second)
	assert.Nil(t, mb)
	assert.Equal(t, common.ErrTimeoutWaitingForBlock, err)
	assert.Positive(t, forgedSaves, "the forged gossip never reached the processor")
}

func TestEpochStartMetaSyncer_ManyOriginatorsThroughOneConnectedPeerShouldNotWinTheTimeoutFallback(t *testing.T) {
	t.Parallel()

	// two originators through one relay used to meet the fallback floor of 2
	mb, forgedSaves, err := runEpochStartGossipScenario(t, 0, 9, 2, 3*time.Second)
	assert.Nil(t, mb)
	assert.Equal(t, common.ErrTimeoutWaitingForBlock, err)
	assert.Positive(t, forgedSaves, "the forged gossip never reached the processor")
}

func TestEpochStartMetaSyncer_ConnectedPeersMajorityShouldReachConsensus(t *testing.T) {
	t.Parallel()

	// 5 honest and 1 relay connected give a target of 5, which the honest peers meet on their own
	mb, _, err := runEpochStartGossipScenario(t, 5, 0, 7, 10*time.Second)
	require.NoError(t, err)
	require.NotNil(t, mb)
	assert.Equal(t, uint32(10), mb.GetEpoch())
}

type prevEpochSyncerStub struct {
	requested      bool
	requestedEpoch uint32
	prevMeta       *block.Block
}

func (s *prevEpochSyncerStub) SyncEpochStartMeta(_ time.Duration) (*block.Block, error) {
	return nil, errors.New("not expected")
}

func (s *prevEpochSyncerStub) SyncPrevEpochStartMeta(_ time.Duration, epoch uint32) (*block.Block, error) {
	s.requested = true
	s.requestedEpoch = epoch
	return s.prevMeta, nil
}

func (s *prevEpochSyncerStub) IsInterfaceNil() bool {
	return s == nil
}

func TestSyncPrevEpochStartMeta_EpochZeroShouldReuseItsOwnMetaBlock(t *testing.T) {
	t.Parallel()

	// epoch 0 - 1 would wrap to the unknown-epoch identifier, which no answer could match
	syncer := &prevEpochSyncerStub{}
	epochStartMeta := &block.Block{Header: &block.BlockHeader{Epoch: 0, IsEpochStart: true}}

	prev, err := syncPrevEpochStartMeta(syncer, epochStartMeta, time.Second)
	require.NoError(t, err)
	assert.Same(t, epochStartMeta, prev)
	assert.False(t, syncer.requested, "epoch 0 must not send a request")
}

func TestSyncPrevEpochStartMeta_ShouldFetchThePreviousEpoch(t *testing.T) {
	t.Parallel()

	prevMeta := &block.Block{Header: &block.BlockHeader{Epoch: 6206, IsEpochStart: true}}
	syncer := &prevEpochSyncerStub{prevMeta: prevMeta}
	epochStartMeta := &block.Block{Header: &block.BlockHeader{Epoch: 6207, IsEpochStart: true}}

	prev, err := syncPrevEpochStartMeta(syncer, epochStartMeta, time.Second)
	require.NoError(t, err)
	assert.Same(t, prevMeta, prev)
	assert.True(t, syncer.requested)
	assert.Equal(t, uint32(6206), syncer.requestedEpoch)
}
