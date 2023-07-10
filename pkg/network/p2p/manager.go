package p2p

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"

	"github.com/iotaledger/hive.go/autopeering/peer"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/iota-core/pkg/network"
)

// ConnectPeerOption defines an option for the DialPeer and AcceptPeer methods.
type ConnectPeerOption func(conf *connectPeerConfig)

type connectPeerConfig struct {
	useDefaultTimeout bool
}

// ProtocolHandler holds callbacks to handle a protocol.
type ProtocolHandler struct {
	PacketFactory func() proto.Message
	PacketHandler func(network.PeerID, proto.Message) error
}

func buildConnectPeerConfig(opts []ConnectPeerOption) *connectPeerConfig {
	conf := &connectPeerConfig{
		useDefaultTimeout: true,
	}
	for _, o := range opts {
		o(conf)
	}

	return conf
}

// WithNoDefaultTimeout returns a ConnectPeerOption that disables the default timeout for dial or accept.
func WithNoDefaultTimeout() ConnectPeerOption {
	return func(conf *connectPeerConfig) {
		conf.useDefaultTimeout = false
	}
}

// The Manager handles the connected neighbors.
type Manager struct {
	local      *peer.Local
	libp2pHost host.Host

	acceptMutex sync.RWMutex
	acceptMap   map[libp2ppeer.ID]*AcceptMatcher

	log                 *logger.Logger
	neighborGroupEvents map[NeighborsGroup]*NeighborGroupEvents

	stopMutex sync.RWMutex
	isStopped bool

	neighbors      map[network.PeerID]*Neighbor
	neighborsMutex sync.RWMutex

	registeredProtocolsMutex sync.RWMutex
	registeredProtocols      map[protocol.ID]*ProtocolHandler
}

// NewManager creates a new Manager.
func NewManager(libp2pHost host.Host, local *peer.Local, log *logger.Logger) *Manager {
	return &Manager{
		libp2pHost: libp2pHost,
		acceptMap:  map[libp2ppeer.ID]*AcceptMatcher{},
		local:      local,
		log:        log,
		neighborGroupEvents: map[NeighborsGroup]*NeighborGroupEvents{
			NeighborsGroupAuto:   NewNeighborGroupEvents(),
			NeighborsGroupManual: NewNeighborGroupEvents(),
		},
		neighbors:           map[network.PeerID]*Neighbor{},
		registeredProtocols: map[protocol.ID]*ProtocolHandler{},
	}
}

// Stop stops the manager and closes all established connections.
func (m *Manager) Stop() {
	m.stopMutex.Lock()
	defer m.stopMutex.Unlock()

	if m.isStopped {
		return
	}
	m.isStopped = true
	m.dropAllNeighbors()
}

// NeighborGroupEvents returns the events related to the neighbor group.
func (m *Manager) NeighborGroupEvents(group NeighborsGroup) *NeighborGroupEvents {
	return m.neighborGroupEvents[group]
}

// LocalPeerID returns the local peer ID.
func (m *Manager) LocalPeerID() network.PeerID {
	return m.local.ID()
}

// RegisterProtocol registers a new protocol.
func (m *Manager) RegisterProtocol(protocolID string, factory func() proto.Message, handler func(network.PeerID, proto.Message) error) {
	m.registeredProtocolsMutex.Lock()
	defer m.registeredProtocolsMutex.Unlock()

	m.registeredProtocols[protocol.ID(protocolID)] = &ProtocolHandler{
		PacketFactory: factory,
		PacketHandler: handler,
	}
	m.libp2pHost.SetStreamHandler(protocol.ID(protocolID), m.handleStream)
}

// UnregisterProtocol unregisters a protocol.
func (m *Manager) UnregisterProtocol(protocolID string) {
	m.registeredProtocolsMutex.Lock()
	defer m.registeredProtocolsMutex.Unlock()

	m.libp2pHost.RemoveStreamHandler(protocol.ID(protocolID))
	delete(m.registeredProtocols, protocol.ID(protocolID))
}

// P2PHost returns the lib-p2p host.
func (m *Manager) P2PHost() host.Host {
	return m.libp2pHost
}

// LocalPeer return the local peer.
func (m *Manager) LocalPeer() *peer.Local {
	return m.local
}

// AddOutbound tries to add a neighbor by connecting to that peer.
func (m *Manager) AddOutbound(ctx context.Context, p *peer.Peer, group NeighborsGroup,
	connectOpts ...ConnectPeerOption,
) error {
	m.log.Debugw("adding outbound neighbor", "peer", p.ID())
	return m.addNeighbor(ctx, p, group, m.dialPeer, connectOpts)
}

// AddInbound tries to add a neighbor by accepting an incoming connection from that peer.
func (m *Manager) AddInbound(ctx context.Context, p *peer.Peer, group NeighborsGroup,
	connectOpts ...ConnectPeerOption,
) error {
	m.log.Debugw("adding inbound neighbor", "peer", p.ID())
	return m.addNeighbor(ctx, p, group, m.acceptPeer, connectOpts)
}

// Neighbor returns the neighbor by its id.
func (m *Manager) Neighbor(id network.PeerID) (*Neighbor, error) {
	m.neighborsMutex.RLock()
	defer m.neighborsMutex.RUnlock()
	nbr, ok := m.neighbors[id]
	if !ok {
		return nil, ErrUnknownNeighbor
	}

	return nbr, nil
}

// DropNeighbor disconnects the neighbor with the given ID and the group.
func (m *Manager) DropNeighbor(id network.PeerID, group NeighborsGroup) error {
	nbr, err := m.neighborWithGroup(id, group)
	if err != nil {
		return ierrors.WithStack(err)
	}
	nbr.Close()

	return nil
}

// Send sends a message with the specific protocol to a set of neighbors.
func (m *Manager) Send(packet proto.Message, protocolID string, to ...network.PeerID) {
	var neighbors []*Neighbor
	if len(to) == 0 {
		neighbors = m.AllNeighbors()
	} else {
		neighbors = m.NeighborsByID(to)
	}

	for _, nbr := range neighbors {
		nbr.Enqueue(packet, protocol.ID(protocolID))
	}
}

// AllNeighbors returns all the neighbors that are currently connected.
func (m *Manager) AllNeighbors() []*Neighbor {
	m.neighborsMutex.RLock()
	defer m.neighborsMutex.RUnlock()
	result := make([]*Neighbor, 0, len(m.neighbors))
	for _, n := range m.neighbors {
		result = append(result, n)
	}

	return result
}

// AllNeighborsIDs returns all the ids of the neighbors that are currently connected.
func (m *Manager) AllNeighborsIDs() (ids []network.PeerID) {
	ids = make([]network.PeerID, 0)
	neighbors := m.AllNeighbors()
	for _, nbr := range neighbors {
		ids = append(ids, nbr.ID())
	}

	return
}

// NeighborsByID returns all the neighbors that are currently connected corresponding to the supplied ids.
func (m *Manager) NeighborsByID(ids []network.PeerID) []*Neighbor {
	result := make([]*Neighbor, 0, len(ids))
	if len(ids) == 0 {
		return result
	}

	m.neighborsMutex.RLock()
	defer m.neighborsMutex.RUnlock()
	for _, id := range ids {
		if n, ok := m.neighbors[id]; ok {
			result = append(result, n)
		}
	}

	return result
}

// neighborWithGroup returns neighbor by ID and group.
func (m *Manager) neighborWithGroup(id network.PeerID, group NeighborsGroup) (*Neighbor, error) {
	m.neighborsMutex.RLock()
	defer m.neighborsMutex.RUnlock()
	nbr, ok := m.neighbors[id]
	if !ok || nbr.Group != group {
		return nil, ErrUnknownNeighbor
	}

	return nbr, nil
}

func (m *Manager) addNeighbor(ctx context.Context, p *peer.Peer, group NeighborsGroup,
	connectorFunc func(context.Context, *peer.Peer, []ConnectPeerOption) (map[protocol.ID]*PacketsStream, error),
	connectOpts []ConnectPeerOption,
) error {
	if p.ID() == m.local.ID() {
		return ierrors.WithStack(ErrLoopbackNeighbor)
	}
	m.stopMutex.RLock()
	defer m.stopMutex.RUnlock()
	if m.isStopped {
		return ErrNotRunning
	}
	if m.neighborExists(p.ID()) {
		return ierrors.WithStack(ErrDuplicateNeighbor)
	}

	streams, err := connectorFunc(ctx, p, connectOpts)
	if err != nil {
		return ierrors.WithStack(err)
	}

	// create and add the neighbor
	nbr := NewNeighbor(p, group, streams, m.log, func(nbr *Neighbor, protocol protocol.ID, packet proto.Message) {
		m.registeredProtocolsMutex.RLock()
		defer m.registeredProtocolsMutex.RUnlock()

		protocolHandler, isRegistered := m.registeredProtocols[protocol]
		if !isRegistered {
			nbr.Log.Errorw("Can't handle packet as the protocol is not registered", "protocol", protocol, "err", err)
		}
		if err := protocolHandler.PacketHandler(nbr.ID(), packet); err != nil {
			nbr.Log.Debugw("Can't handle packet", "err", err)
		}
	}, func(nbr *Neighbor) {
		m.deleteNeighbor(nbr)
		m.NeighborGroupEvents(nbr.Group).NeighborRemoved.Trigger(&NeighborRemovedEvent{nbr})
	})
	if err := m.setNeighbor(nbr); err != nil {
		for _, ps := range streams {
			if resetErr := ps.Close(); resetErr != nil {
				nbr.Log.Errorw("error closing stream", "err", resetErr)
			}
		}

		return ierrors.WithStack(err)
	}
	nbr.readLoop()
	nbr.writeLoop()
	nbr.Log.Info("Connection established")
	m.neighborGroupEvents[group].NeighborAdded.Trigger(&NeighborAddedEvent{nbr})

	return nil
}

func (m *Manager) neighborExists(id network.PeerID) bool {
	m.neighborsMutex.RLock()
	defer m.neighborsMutex.RUnlock()
	_, exists := m.neighbors[id]

	return exists
}

func (m *Manager) deleteNeighbor(nbr *Neighbor) {
	m.neighborsMutex.Lock()
	defer m.neighborsMutex.Unlock()
	delete(m.neighbors, nbr.ID())
}

func (m *Manager) setNeighbor(nbr *Neighbor) error {
	m.neighborsMutex.Lock()
	defer m.neighborsMutex.Unlock()
	if _, exists := m.neighbors[nbr.ID()]; exists {
		return ierrors.WithStack(ErrDuplicateNeighbor)
	}
	m.neighbors[nbr.ID()] = nbr

	return nil
}

func (m *Manager) dropAllNeighbors() {
	neighborsList := m.AllNeighbors()
	for _, nbr := range neighborsList {
		nbr.Close()
	}
}
