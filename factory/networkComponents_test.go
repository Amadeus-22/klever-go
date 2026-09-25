package factory

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/network/p2p"
	"github.com/klever-io/klever-go/network/p2p/libp2p"
	"github.com/klever-io/klever-go/network/p2p/libp2p/p2pkey"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/require"
)

func createP2pPrivateKey(t *testing.T) libp2pCrypto.PrivKey {
	sk, err := p2pkey.Generate()
	require.NoError(t, err)

	return sk
}

func TestNewNetworkComponentsFactory_NilStatusHandlerShouldErr(t *testing.T) {
	t.Parallel()

	ncf, err := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		nil,
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		createP2pPrivateKey(t),
	)
	require.Nil(t, ncf)
	require.Equal(t, common.ErrNilStatusHandler, err)
}

func TestNewNetworkComponentsFactory_NilMarshalizerShouldErr(t *testing.T) {
	t.Parallel()

	ncf, err := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		&mock.AppStatusHandlerMock{},
		nil,
		&libp2p.LocalSyncTimer{},
		createP2pPrivateKey(t),
	)
	require.Nil(t, ncf)
	require.True(t, errors.Is(err, common.ErrNilMarshalizer))
}

func TestNewNetworkComponentsFactory_NilP2pPrivateKeyShouldErr(t *testing.T) {
	t.Parallel()

	ncf, err := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		&mock.AppStatusHandlerMock{},
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		nil,
	)
	require.Nil(t, ncf)
	require.True(t, errors.Is(err, p2p.ErrNilP2pPrivateKey))
}

func TestNewNetworkComponentsFactory_TypedNilP2pPrivateKeyShouldErr(t *testing.T) {
	t.Parallel()

	var typedNil *libp2pCrypto.Secp256k1PrivateKey
	ncf, err := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		&mock.AppStatusHandlerMock{},
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		typedNil,
	)
	require.Nil(t, ncf)
	require.True(t, errors.Is(err, p2p.ErrNilP2pPrivateKey))
}

func TestNewNetworkComponentsFactory_OkValsShouldWork(t *testing.T) {
	t.Parallel()

	ncf, err := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		&mock.AppStatusHandlerMock{},
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		createP2pPrivateKey(t),
	)
	require.NoError(t, err)
	require.NotNil(t, ncf)
}

func TestNetworkComponentsFactory_Create_ShouldErrDueToBadConfig(t *testing.T) {
	ncf, _ := NewNetworkComponentsFactory(
		config.Config{
			P2P: config.P2PConfig{},
		},
		&mock.AppStatusHandlerMock{},
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		createP2pPrivateKey(t),
	)

	nc, err := ncf.Create()
	require.Error(t, err)
	require.Nil(t, nc)
}

func TestNetworkComponentsFactory_Create_ShouldWork(t *testing.T) {
	p2pConfig := config.P2PConfig{
		Node: config.NodeConfig{
			Port: "0",
		},
		KadDhtPeerDiscovery: config.KadDhtPeerDiscoveryConfig{
			Enabled:                          false,
			RefreshIntervalInSec:             10,
			ProtocolID:                       "klv/kad/1.0.0",
			InitialPeerList:                  []string{"peer0", "peer1"},
			BucketSize:                       10,
			RoutingTableRefreshIntervalInSec: 5,
		},
		Sharding: config.ShardingConfig{
			TargetPeerCount:         10,
			MaxIntraShardValidators: 10,
			MaxCrossShardValidators: 10,
			MaxIntraShardObservers:  10,
			MaxCrossShardObservers:  10,
			Type:                    "NilListSharder",
		},
	}
	ncf, _ := NewNetworkComponentsFactory(
		config.Config{
			P2P: p2pConfig,
			Debug: config.DebugConfig{
				Antiflood: config.AntifloodDebugConfig{
					Enabled:                    true,
					CacheSize:                  100,
					IntervalAutoPrintInSeconds: 1,
				},
			},
		},
		&mock.AppStatusHandlerMock{},
		&mock.MarshalizerMock{},
		&libp2p.LocalSyncTimer{},
		createP2pPrivateKey(t),
	)

	ncf.SetListenAddress(libp2p.ListenLocalhostAddrWithIp4AndTcp)

	nc, err := ncf.Create()
	require.NoError(t, err)
	require.NotNil(t, nc)
}
