//go:build dockertests

package dockertestframework

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
	"github.com/iotaledger/iota.go/v4/nodeclient"
	"github.com/iotaledger/iota.go/v4/wallet"
)

type Node struct {
	Name                  string
	ContainerName         string
	ClientURL             string
	AccountAddressBech32  string
	ContainerConfigs      string
	PrivateKey            string
	IssueCandidacyPayload bool
	DatabasePath          string
	SnapshotPath          string
}

func (n *Node) AccountAddress(t *testing.T) *iotago.AccountAddress {
	_, addr, err := iotago.ParseBech32(n.AccountAddressBech32)
	require.NoError(t, err)
	accAddress, ok := addr.(*iotago.AccountAddress)
	require.True(t, ok)

	return accAddress
}

func (d *DockerTestFramework) NodeStatus(name string) *api.InfoResNodeStatus {
	node := d.Node(name)

	info, err := d.Client(node.Name).Info(context.TODO())
	require.NoError(d.Testing, err)

	return info.Status
}

func (d *DockerTestFramework) waitForNodesAndGetClients() error {
	nodes := d.Nodes()

	d.nodesLock.Lock()
	defer d.nodesLock.Unlock()

	for _, node := range nodes {
		client, err := nodeclient.New(node.ClientURL)
		if err != nil {
			return ierrors.Wrapf(err, "failed to create node client for node %s", node.Name)
		}
		d.nodes[node.Name] = node
		d.clients[node.Name] = client
	}

	d.defaultWallet = mock.NewWallet(
		d.Testing,
		"default",
		d.clients["V1"],
		&DockerWalletClock{client: d.clients["V1"]},
		lo.PanicOnErr(wallet.NewKeyManagerFromRandom(wallet.DefaultIOTAPath)),
	)

	return nil
}

func (d *DockerTestFramework) WaitUntilNetworkReady() {
	d.WaitUntilNetworkHealthy()

	// inx-faucet is up only when the node and indexer are healthy, thus need to check the faucet even after nodes are synced.
	d.WaitUntilFaucetHealthy()

	d.DumpContainerLogsToFiles()
}

func (d *DockerTestFramework) WaitUntilNetworkHealthy() {
	fmt.Println("Wait until the network is healthy...")
	defer fmt.Println("Wait until the network is healthy......done")

	d.Eventually(func() error {
		for _, node := range d.Nodes() {
			for {
				info, err := d.Client(node.Name).Info(context.TODO())
				if err != nil {
					return err
				}

				if info.Status.IsNetworkHealthy {
					fmt.Println("Node", node.Name, "is synced")
					break
				}
			}
		}

		return nil
	}, true)
}

func (d *DockerTestFramework) AddValidatorNode(name string, containerName string, clientURL string, accAddrBech32 string, optIssueCandidacyPayload ...bool) {
	d.nodesLock.Lock()
	defer d.nodesLock.Unlock()

	issueCandidacyPayload := true
	if len(optIssueCandidacyPayload) > 0 {
		issueCandidacyPayload = optIssueCandidacyPayload[0]
	}

	d.nodes[name] = &Node{
		Name:                  name,
		ContainerName:         containerName,
		ClientURL:             clientURL,
		AccountAddressBech32:  accAddrBech32,
		IssueCandidacyPayload: issueCandidacyPayload,
	}
}

func (d *DockerTestFramework) AddNode(name string, containerName string, clientURL string) {
	d.nodesLock.Lock()
	defer d.nodesLock.Unlock()

	d.nodes[name] = &Node{
		Name:          name,
		ContainerName: containerName,
		ClientURL:     clientURL,
	}
}

func (d *DockerTestFramework) Nodes(names ...string) []*Node {
	d.nodesLock.RLock()
	defer d.nodesLock.RUnlock()

	if len(names) == 0 {
		nodes := make([]*Node, 0, len(d.nodes))
		for _, node := range d.nodes {
			nodes = append(nodes, node)
		}

		return nodes
	}

	nodes := make([]*Node, len(names))
	for i, name := range names {
		nodes[i] = d.Node(name)
	}

	return nodes
}

func (d *DockerTestFramework) Node(name string) *Node {
	d.nodesLock.RLock()
	defer d.nodesLock.RUnlock()

	node, exist := d.nodes[name]
	require.True(d.Testing, exist)

	return node
}

func (d *DockerTestFramework) ModifyNode(name string, fun func(*Node)) {
	d.nodesLock.Lock()
	defer d.nodesLock.Unlock()

	node, exist := d.nodes[name]
	require.True(d.Testing, exist)

	fun(node)
}

// Restarts a node with another database path, conceptually deleting the database and
// restarts it with the given snapshot path.
func (d *DockerTestFramework) ResetNode(alias string, newSnapshotPath string) {
	fmt.Println("Reset node", alias)

	d.ModifyNode(alias, func(n *Node) {
		n.DatabasePath = fmt.Sprintf("/app/database/database%d", rand.Int())
		n.SnapshotPath = newSnapshotPath
	})
	d.DockerComposeUp(true)
	d.DumpContainerLog(d.Node(alias).ContainerName, "reset1")
	d.WaitUntilNetworkHealthy()
}

func (d *DockerTestFramework) RequestFromNodes(testFunc func(*testing.T, string)) {
	for nodeAlias := range d.nodes {
		testFunc(d.Testing, nodeAlias)
	}
}