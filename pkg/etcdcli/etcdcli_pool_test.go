package etcdcli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/tests/v3/integration"
)

// rather poor men's approach to mocking
type clientPoolRecorder struct {
	pool *EtcdClientPool

	numNewCalls      int
	numEndpointCalls int
	numHealthCalls   int
	numCloseCalls    int

	newFuncErrReturn     error
	healthCheckErrReturn error
	endpointErrReturn    error
	closeFuncErrReturn   error
	updatedEndpoints     []string
}

func TestClientGetReturnHappyPath(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)

	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientEndpointFailureReturnsImmediately(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	poolRecorder.endpointErrReturn = errors.New("fail")

	client, err := poolRecorder.pool.Get()
	require.Error(t, err, "expected endpoint error fail, but got %w", err)
	assert.Nil(t, client)
	assert.Equal(t, 0, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 0, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientDoubleGetReturnsNewClient(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)

	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
	// not returning the given client
	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 2, poolRecorder.numNewCalls)
	assert.Equal(t, 2, poolRecorder.numEndpointCalls)
	assert.Equal(t, 2, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientReusesClientsReturned(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)

	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
	poolRecorder.pool.Return(client)
	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 2, poolRecorder.numEndpointCalls)
	assert.Equal(t, 2, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientClosesOnChannelCapacity(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	var clients []*clientv3.Client
	for i := 0; i < maxNumCachedClients+1; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
		clients = append(clients, client)
	}

	// returning all should make sure the last one tripping over capacity should get closed
	for _, client := range clients {
		poolRecorder.pool.Return(client)
	}

	assert.Equal(t, maxNumCachedClients+1, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumCachedClients+1, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumCachedClients+1, poolRecorder.numHealthCalls)
	assert.Equal(t, 1, poolRecorder.numCloseCalls)
}

func TestNewClientWithOpenClients(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	var clients []*clientv3.Client
	for i := 0; i < maxNumOpenClients; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
		clients = append(clients, client)
	}

	assert.Equal(t, maxNumOpenClients, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)

	// this should block and return an error
	client, err := poolRecorder.pool.Get()
	assert.Nil(t, client)
	assert.Errorf(t, err, "too many active cache clients, rejecting to create new one")
	// returning one should unlock the get again
	poolRecorder.pool.Return(clients[0])
	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numNewCalls)        // no new call added
	assert.Equal(t, maxNumOpenClients+2, poolRecorder.numEndpointCalls) // called Get twice additionally
	assert.Equal(t, maxNumOpenClients+1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClosesReturnOpenClients(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	var clients []*clientv3.Client
	for i := 0; i < maxNumOpenClients; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
		clients = append(clients, client)
	}

	assert.Equal(t, maxNumOpenClients, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)

	// return all clients to fill the internal cache and cause five to close
	for i := 0; i < maxNumOpenClients; i++ {
		poolRecorder.pool.Return(clients[i])
	}
	assert.Equal(t, maxNumCachedClients, poolRecorder.numCloseCalls)

	// now we should be able to get the full amount of clients again
	for i := 0; i < maxNumOpenClients; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
	}

	// replenish the maxNumCachedClients that were closed earlier
	assert.Equal(t, maxNumOpenClients+maxNumCachedClients, poolRecorder.numNewCalls)
	// no open clients are available anymore, as we have handed out all clients
	assert.Equal(t, 0, len(poolRecorder.pool.availableOpenClients))
	assert.Equal(t, maxNumOpenClients*2, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients*2, poolRecorder.numHealthCalls)
	assert.Equal(t, maxNumCachedClients, poolRecorder.numCloseCalls)
}

func TestClosesReturnOpenClientCloseError(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	var clients []*clientv3.Client
	for i := 0; i < maxNumOpenClients; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
		clients = append(clients, client)
	}

	assert.Equal(t, maxNumOpenClients, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)

	// return all clients to fill the internal cache and cause five to close
	// first close should fail, but not impact anything and certainly not block
	poolRecorder.closeFuncErrReturn = errors.New("fail")
	for i := 0; i < maxNumOpenClients; i++ {
		poolRecorder.pool.Return(clients[i])
	}
	assert.Equal(t, maxNumCachedClients, poolRecorder.numCloseCalls)

	// now we should be able to get the full amount of clients again
	for i := 0; i < maxNumOpenClients; i++ {
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
	}

	// replenish the maxNumCachedClients that were closed earlier
	assert.Equal(t, maxNumOpenClients+maxNumCachedClients, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumOpenClients*2, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients*2, poolRecorder.numHealthCalls)
	assert.Equal(t, maxNumCachedClients, poolRecorder.numCloseCalls)
}

// this scenario used to lock-up etcd on start-up a lot, as the client does some initial connection testing that may fail
// eventually it will be exhausting all the openClient quota
func TestFailingOnCreationReturnsClients(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	// this should already happen at maxNumOpenClients/numRetries, so we're testing this is working pretty well here.
	for i := 0; i < maxNumOpenClients; i++ {
		// this error should fail the first retry consistently
		poolRecorder.newFuncErrReturn = fmt.Errorf("constant error")
		client, err := poolRecorder.pool.Get()
		require.NoError(t, err)
		assert.NotNil(t, client)
	}

	// replenish the maxNumCachedClients that were closed earlier
	assert.Equal(t, maxNumOpenClients*2, poolRecorder.numNewCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numEndpointCalls)
	assert.Equal(t, maxNumOpenClients, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientClosesAndCreatesOnError(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
	poolRecorder.pool.Return(client)

	poolRecorder.healthCheckErrReturn = fmt.Errorf("some error")

	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 2, poolRecorder.numNewCalls)
	assert.Equal(t, 2, poolRecorder.numEndpointCalls)
	// 3 calls, since we first test the returned client resulting in failure, then we test the new client
	assert.Equal(t, 3, poolRecorder.numHealthCalls)
	assert.Equal(t, 1, poolRecorder.numCloseCalls)
}

func TestClientHealthCheckCloseErrorRetriesAndReturnsClient(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
	poolRecorder.pool.Return(client)

	poolRecorder.healthCheckErrReturn = fmt.Errorf("some health error")
	poolRecorder.closeFuncErrReturn = fmt.Errorf("some close error")

	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 2, poolRecorder.numNewCalls)
	assert.Equal(t, 2, poolRecorder.numEndpointCalls)
	// 3 calls, since we first test the returned client resulting in failure, then we test the new client
	assert.Equal(t, 3, poolRecorder.numHealthCalls)
	assert.Equal(t, 1, poolRecorder.numCloseCalls)
}

func TestClientUpdatesEndpoints(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	client, err := poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 1, poolRecorder.numEndpointCalls)
	assert.Equal(t, 1, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
	poolRecorder.pool.Return(client)

	// by default, we're using client 0 going to m0, client 1 should go to m1
	expectedEndpoints := testServer.Client(1).Endpoints()
	poolRecorder.updatedEndpoints = expectedEndpoints

	client, err = poolRecorder.pool.Get()
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, expectedEndpoints, client.Endpoints())
	assert.Equal(t, 1, poolRecorder.numNewCalls)
	assert.Equal(t, 2, poolRecorder.numEndpointCalls)
	assert.Equal(t, 2, poolRecorder.numHealthCalls)
	assert.Equal(t, 0, poolRecorder.numCloseCalls)
}

func TestClientOpenClientReturnNil(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	// should not panic
	assert.NotPanics(t, func() {
		poolRecorder.pool.Return(nil)
	})
}

// we try to return many more clients than we actually handed out, this should fill the pool but not block when it's full
func TestClientOpenClientMultiReturns(t *testing.T) {
	integration.BeforeTestExternal(t)
	testServer := integration.NewClusterV3(t, &integration.ClusterConfig{Size: 3})
	defer testServer.Terminate(t)

	poolRecorder := newTestPool(testServer)
	for i := 0; i < maxNumOpenClients*3; i++ {
		poolRecorder.pool.Return(testServer.RandClient())
	}
	assert.Equal(t, 0, poolRecorder.numNewCalls)
	assert.Equal(t, 0, poolRecorder.numEndpointCalls)
	assert.Equal(t, 0, poolRecorder.numHealthCalls)
	assert.Equal(t, maxNumOpenClients*3-maxNumCachedClients, poolRecorder.numCloseCalls)
}

func newTestPool(testServer *integration.ClusterV3) *clientPoolRecorder {
	rec := &clientPoolRecorder{}
	endpointFunc := func() ([]string, error) {
		rec.numEndpointCalls++
		if rec.updatedEndpoints != nil {
			endpoints := rec.updatedEndpoints
			rec.updatedEndpoints = nil
			return endpoints, nil
		}

		if rec.endpointErrReturn != nil {
			err := rec.endpointErrReturn
			rec.endpointErrReturn = nil
			return nil, err
		}

		return testServer.Client(0).Endpoints(), nil
	}

	newFunc := func() (*clientv3.Client, error) {
		rec.numNewCalls++
		if rec.newFuncErrReturn != nil {
			err := rec.newFuncErrReturn
			rec.newFuncErrReturn = nil
			return nil, err
		}
		return testServer.Client(0), nil
	}

	healthFunc := func(client *clientv3.Client) error {
		rec.numHealthCalls++
		err := rec.healthCheckErrReturn
		rec.healthCheckErrReturn = nil
		return err
	}

	closeFunc := func(client *clientv3.Client) error {
		rec.numCloseCalls++
		err := rec.closeFuncErrReturn
		rec.closeFuncErrReturn = nil
		return err
	}

	rec.pool = NewEtcdClientPool(newFunc, endpointFunc, healthFunc, closeFunc)
	return rec
}
