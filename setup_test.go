package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipfs/boxo/gateway"
	blocks "github.com/ipfs/go-block-format"
	ci "github.com/libp2p/go-libp2p-testing/ci"
	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

func mustFreePort(t *testing.T) (int, *net.TCPListener) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	require.NoError(t, err)

	l, err := net.ListenTCP("tcp", addr)
	require.NoError(t, err)

	return l.Addr().(*net.TCPAddr).Port, l
}

func mustFreePorts(t *testing.T, n int) []int {
	ports := make([]int, 0)
	for range n {
		port, listener := mustFreePort(t)
		defer listener.Close()
		ports = append(ports, port)
	}

	return ports
}

func mustListenAddrWithPort(t *testing.T, port int) multiaddr.Multiaddr {
	ma, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port))
	require.NoError(t, err)
	return ma
}

// mustPeeredNodes creates a network of [Node]s with the given configuration.
// The configuration contains as many elements as there are nodes. Each element
// indicates to which other nodes it is connected.
//
//	Example configuration: [][]int{
//	 {1, 2},
//	 {0},
//	 {0},
//	}
//
// - Node 0 is connected to nodes 1 and 2.
// - Node 1 is connected to node 0.
// - Node 2 is connected to node 1.
func mustPeeredNodes(t *testing.T, configuration [][]int, peeringShareCache bool) []*Node {
	n := len(configuration)

	// Generate ports, secrets keys, peer IDs and multiaddresses.
	ports := mustFreePorts(t, n)
	keys := make([]ic.PrivKey, n)
	pids := make([]peer.ID, n)
	mas := make([]multiaddr.Multiaddr, n)
	addrInfos := make([]peer.AddrInfo, n)

	for i := range n {
		keys[i], pids[i] = mustTestPeer(t)
		mas[i] = mustListenAddrWithPort(t, ports[i])
		addrInfos[i] = peer.AddrInfo{
			ID:    pids[i],
			Addrs: []multiaddr.Multiaddr{mas[i]},
		}
	}

	cfgs := make([]Config, n)
	nodes := make([]*Node, n)
	for i := range n {
		cfgs[i] = Config{
			DHTRouting:         DHTOff,
			RoutingV1Endpoints: []string{},
			ListenAddrs:        []string{mas[i].String()},
			Peering:            []peer.AddrInfo{},
			PeeringSharedCache: peeringShareCache,
			Bitswap:            true,
		}

		for _, j := range configuration[i] {
			cfgs[i].Peering = append(cfgs[i].Peering, addrInfos[j])
		}

		nodes[i] = mustTestNodeWithKey(t, cfgs[i], keys[i])

		t.Log("Node", i, "Addresses", nodes[i].host.Addrs(), "Peering", cfgs[i].Peering)
	}

	require.Eventually(t, func() bool {
		for i, node := range nodes {
			for _, peer := range cfgs[i].Peering {
				if node.host.Network().Connectedness(peer.ID) != network.Connected {
					return false
				}
			}
		}

		return true
	}, time.Second*30, time.Millisecond*100)

	return nodes
}

func TestPeering(t *testing.T) {
	_ = mustPeeredNodes(t, [][]int{
		{1, 2},
		{0, 2},
		{0, 1},
	}, false)
}

func TestSetupWithLibp2pDHTOffDoesNotExposeDelegatedDiscovery(t *testing.T) {
	node := mustTestNode(t, Config{
		Bitswap:            true,
		DHTRouting:         DHTOff,
		RoutingV1Endpoints: []string{"https://router.example/routing/v1/providers"},
	})
	require.Nil(t, node.contentDiscovery)
}

func TestLocalBlockstoreCapacityConfiguredThroughSetup(t *testing.T) {
	node := mustTestNode(t, Config{Bitswap: true, BlockstoreMaxSize: 4})
	require.NotNil(t, node.capacityMetadata)
	require.NotEqual(t, node.datastore, node.capacityMetadata)
	_, err := os.Stat(filepath.Join(node.dataDir, "capacity-metadata", "flatfs"))
	require.NoError(t, err)
	ctx := t.Context()
	blocksToPut := []blocks.Block{
		blocks.NewBlock([]byte("aa")),
		blocks.NewBlock([]byte("bb")),
		blocks.NewBlock([]byte("cc")),
	}
	for _, block := range blocksToPut {
		require.NoError(t, node.blockstore.Put(ctx, block))
	}

	has, err := node.blockstore.Has(ctx, blocksToPut[0].Cid())
	require.NoError(t, err)
	require.False(t, has)
	has, err = node.blockstore.Has(ctx, blocksToPut[2].Cid())
	require.NoError(t, err)
	require.True(t, has)
	keys, err := node.blockstore.AllKeysChan(ctx)
	require.NoError(t, err)
	var keyCount int
	for range keys {
		keyCount++
	}
	require.Equal(t, 2, keyCount)
}

func TestLocalBlockstoreZeroCapacityIsUnlimitedThroughSetup(t *testing.T) {
	node := mustTestNode(t, Config{Bitswap: true})
	ctx := t.Context()
	for _, data := range []string{"aa", "bb", "cc"} {
		block := blocks.NewBlock([]byte(data))
		require.NoError(t, node.blockstore.Put(ctx, block))
		has, err := node.blockstore.Has(ctx, block.Cid())
		require.NoError(t, err)
		require.True(t, has)
	}
}

func TestLocalBlockstoreCapacityAcrossConfiguredDatastores(t *testing.T) {
	for _, blockstoreType := range []string{"flatfs", "pebble", "badger"} {
		t.Run(blockstoreType, func(t *testing.T) {
			node := mustTestNode(t, Config{BlockstoreType: blockstoreType, Bitswap: true, BlockstoreMaxSize: 2})
			first := blocks.NewBlock([]byte("aa"))
			second := blocks.NewBlock([]byte("bb"))
			require.NoError(t, node.blockstore.Put(t.Context(), first))
			require.NoError(t, node.blockstore.Put(t.Context(), second))
			has, err := node.blockstore.Has(t.Context(), first.Cid())
			require.NoError(t, err)
			require.False(t, has)
		})
	}
}

func TestNodeCloseIsIdempotent(t *testing.T) {
	node := mustTestNode(t, Config{Bitswap: true, BlockstoreMaxSize: 4})
	require.NoError(t, node.Close())
	require.NoError(t, node.Close())
}

func TestPeeringSharedCache(t *testing.T) {
	nodes := mustPeeredNodes(t, [][]int{
		{1}, // 0 peered to 1
		{0}, // 1 peered to 0
		{},  // 2 not peered to anyone
	}, true)

	bl := blocks.NewBlock([]byte(string("peering-cache-test")))

	ctx := t.Context()

	checkBitswap := func(i int, success bool) {
		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()

		_, err := nodes[i].bsrv.GetBlock(ctx, bl.Cid())
		if success {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}

	err := nodes[0].bsrv.AddBlock(ctx, bl)
	require.NoError(t, err)

	// confirm peering enables cache sharing, and bitswap retrieval from safe-listed node works
	checkBitswap(1, true)
	// confirm bitswap providing is disabled by default (no peering)
	checkBitswap(2, false)
}

func testSeedPeering(t *testing.T, n int, dhtRouting DHTRouting, dhtSharedHost bool) ([]ic.PrivKey, []peer.ID, []*Node) {
	cdns := newCachedDNS(dnsCacheRefreshInterval)
	t.Cleanup(func() {
		require.NoError(t, cdns.Close())
	})

	ctx := t.Context()

	seed, err := newSeed()
	require.NoError(t, err)

	keys := make([]ic.PrivKey, n)
	pids := make([]peer.ID, n)
	ports := mustFreePorts(t, n)
	listenAddrs := make([]multiaddr.Multiaddr, n)

	for i := range n {
		keys[i], pids[i] = mustTestPeerFromSeed(t, seed, i)
		listenAddrs[i] = mustListenAddrWithPort(t, ports[i])
	}
	node0P2P, err := multiaddr.NewMultiaddr("/p2p/" + pids[0].String())
	require.NoError(t, err)
	bootstrapPeer := listenAddrs[0].Encapsulate(node0P2P).String()
	bootstrapPeers := []string{bootstrapPeer}
	for i := 1; i < n; i++ {
		p2pAddr, err := multiaddr.NewMultiaddr("/p2p/" + pids[i].String())
		require.NoError(t, err)
		bootstrapPeers = append(bootstrapPeers, listenAddrs[i].Encapsulate(p2pAddr).String())
	}

	cfgs := make([]Config, n)
	nodes := make([]*Node, n)

	for i := range n {
		dnslinkResolver, err := gateway.NewDNSResolver(nil)
		require.NoError(t, err)

		cfgs[i] = Config{
			DataDir:             t.TempDir(),
			BlockstoreType:      "flatfs",
			DHTRouting:          dhtRouting,
			DHTSharedHost:       dhtSharedHost,
			Bitswap:             true,
			Bootstrap:           bootstrapPeers,
			ListenAddrs:         []string{listenAddrs[i].String()},
			Seed:                seed,
			SeedIndex:           i,
			SeedPeering:         true,
			SeedPeeringMaxIndex: n,
			DNSLinkResolver:     dnslinkResolver,
		}

		nodes[i], err = SetupWithLibp2p(ctx, cfgs[i], keys[i], cdns)
		require.NoError(t, err)
		if dhtRouting != DHTOff {
			require.NotNil(t, nodes[i].dhtHost)
			// nodes[i].host may be wrapped in a routed-host by libp2p.New
			// (since we pass libp2p.Routing), so comparing the hosts
			// themselves for identity is unreliable. Comparing the
			// underlying Network() is: RoutedHost.Network() forwards to
			// the wrapped host's Network(), so this reflects whether the
			// DHT host and the main host share the same underlying host.
			if dhtSharedHost {
				require.Same(t, nodes[i].host.Network(), nodes[i].dhtHost.Network())
			} else {
				require.NotSame(t, nodes[i].host.Network(), nodes[i].dhtHost.Network())
			}
		}
		registerTestNodeCleanup(t, nodes[i])
	}

	require.Eventually(t, func() bool {
		for i, node := range nodes {
			for j, pid := range pids {
				if i == j {
					continue
				}

				if node.host.Network().Connectedness(pid) != network.Connected {
					return false
				}
			}
		}

		return true
	}, time.Second*120, time.Millisecond*100)

	return keys, pids, nodes
}

func TestSeedPeering(t *testing.T) {
	if ci.IsRunning() {
		t.Skip("don't run seed peering tests in ci")
	}

	t.Run("DHT disabled", func(t *testing.T) {
		testSeedPeering(t, 3, DHTOff, false)
	})

	t.Run("DHT enabled with shared host disabled", func(t *testing.T) {
		testSeedPeering(t, 3, DHTStandard, false)
	})

	t.Run("DHT enabled with shared host enabled", func(t *testing.T) {
		testSeedPeering(t, 3, DHTStandard, true)
	})
}
