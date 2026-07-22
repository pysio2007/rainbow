package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupRoutingDHTOffDoesNotExposeDelegatedContentDiscovery(t *testing.T) {
	dnsCache := newCachedDNS(dnsCacheRefreshInterval)
	defer dnsCache.Close()

	cr, _, _, dhtHost, dhtDiscovery, err := setupRouting(context.Background(), Config{
		DHTRouting:         DHTOff,
		RoutingV1Endpoints: []string{"https://router.example/routing/v1/providers"},
	}, nil, nil, nil, nil, dnsCache)
	require.NoError(t, err)
	require.NotNil(t, cr)
	require.Nil(t, dhtHost)
	require.Nil(t, dhtDiscovery)
}
