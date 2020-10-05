package membership

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"github.com/youngbupark/raft-membership/pkg/membership/fsm"
	"github.com/youngbupark/raft-membership/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/youngbupark/raft-membership/pkg/proto"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
)

const (
	raftStatePath     = "raft/"
	snapshotsRetained = 2

	// raftLogCacheSize is the maximum number of logs to cache in-memory.
	// This is used to reduce disk I/O for the recently committed entries.
	raftLogCacheSize = 512

	// raftRemoveGracePeriod is how long we wait to allow a RemovePeer
	// to replicate to gracefully leave the cluster.
	raftRemoveGracePeriod = 5 * time.Second
)

type PeerInfo struct {
	Name    string
	Address string
}

type Server struct {
	fsm *fsm.FSM

	id          string
	environment string
	bootstrap   bool
	raftBind    string
	peers       []PeerInfo

	raft          *raft.Raft
	raftLayer     *RaftLayer
	raftStore     *raftboltdb.BoltStore
	raftTransport *raft.NetworkTransport
	raftInmem     *raft.InmemStore

	// raftNotifyCh is set up by setupRaft() and ensures that we get reliable leader
	// transition notifications from the Raft layer.
	raftNotifyCh <-chan bool

	// shutdown and the associated members here are used in orchestrating
	// a clean shutdown. The shutdownCh is never written to, only closed to
	// indicate a shutdown has been initiated.
	shutdown     bool
	shutdownCh   chan struct{}
	shutdownLock sync.Mutex
}

func NewServer(id, raftBind, environment string, peers []PeerInfo, bootstrap bool) *Server {
	return &Server{
		id:          id,
		environment: environment,
		bootstrap:   bootstrap,
		raftBind:    raftBind,
		peers:       peers,
	}
}

func (s *Server) raftStorePath() string {
	return fmt.Sprintf("log-%s", s.id)
}

func (s *Server) InitRaft() error {
	// If we have an unclean exit then attempt to close the Raft store.
	defer func() {
		if s.raft == nil && s.raftStore != nil {
			if err := s.raftStore.Close(); err != nil {
				fmt.Printf("error: %v", err)
			}
		}
	}()

	s.fsm = fsm.New()

	/*
		// For custom transport for tls-enabled.
		// Create a transport layer.
		transConfig := &raft.NetworkTransportConfig{
			Stream:                s.raftLayer,
			MaxPool:               3,
			Timeout:               10 * time.Second,
			ServerAddressProvider: serverAddressProvider,
		}

		trans := raft.NewNetworkTransportWithConfig(transConfig)
	*/

	addr, err := net.ResolveTCPAddr("tcp", s.raftBind)
	if err != nil {
		return err
	}

	trans, err := raft.NewTCPTransport(s.raftBind, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return err
	}

	s.raftTransport = trans

	// Build an all in-memory setup for dev mode, otherwise prepare a full
	// disk-based setup.
	var log raft.LogStore
	var stable raft.StableStore
	var snap raft.SnapshotStore

	if s.environment == "dev" {
		store := raft.NewInmemStore()
		s.raftInmem = store
		stable = store
		log = store
		snap = raft.NewInmemSnapshotStore()
	} else {
		if err := utils.EnsureDir(s.raftStorePath()); err != nil {
			fmt.Printf("Directory creation is failed: %v", err)
			os.Exit(1)
		}

		// Create the backend raft store for logs and stable storage.
		store, err := raftboltdb.NewBoltStore(filepath.Join(s.raftStorePath(), "raft.db"))
		if err != nil {
			return err
		}
		s.raftStore = store
		stable = store

		// Wrap the store in a LogCache to improve performance.
		cacheStore, err := raft.NewLogCache(raftLogCacheSize, store)
		if err != nil {
			return err
		}
		log = cacheStore

		// Create the snapshot store.
		snapshots, err := raft.NewFileSnapshotStore(s.raftStorePath(), snapshotsRetained, os.Stderr)
		if err != nil {
			return err
		}
		snap = snapshots
	}

	// Setup Raft configuration.
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(s.id)

	// If we are in bootstrap or dev mode and the state is clean then we can
	// bootstrap now.
	if s.environment == "dev" || s.bootstrap {
		hasState, err := raft.HasExistingState(log, stable, snap)
		if err != nil {
			return err
		}
		if !hasState {
			configuration := raft.Configuration{
				Servers: []raft.Server{
					{
						ID:      config.LocalID,
						Address: trans.LocalAddr(),
					},
				},
			}

			if err := raft.BootstrapCluster(config,
				log, stable, snap, trans, configuration); err != nil {
				return err
			}
		}
	}

	// Set up a channel for reliable leader notifications.
	s.raftNotifyCh = make(chan bool, 10)
	s.raft, err = raft.NewRaft(config, s.fsm, log, stable, snap, trans)
	go s.peerMonitoring()

	fmt.Println("raft is setup")

	return err
}

func (s *Server) FSM() *fsm.FSM {
	return s.fsm
}

func (s *Server) joinCluster(nodeID, addr string) error {
	fmt.Printf("received join request for remote node %s at %s\n", nodeID, addr)

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		fmt.Printf("failed to get raft configuration: %v\n", err)
		return err
	}

	for _, srv := range configFuture.Configuration().Servers {
		// If a node already exists with either the joining node's ID or address,
		// that node may need to be removed from the config first.
		if srv.ID == raft.ServerID(nodeID) || srv.Address == raft.ServerAddress(addr) {
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(nodeID) {
				fmt.Printf("node %s at %s already member of cluster, ignoring join request\n", nodeID, addr)
				return nil
			}

			future := s.raft.RemoveServer(srv.ID, 0, 0)
			if err := future.Error(); err != nil {
				return fmt.Errorf("error removing existing node %s at %s: %s", nodeID, addr, err)
			}
		}
	}

	f := s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if f.Error() != nil {
		return f.Error()
	}
	fmt.Printf("node %s at %s joined successfully", nodeID, addr)
	return nil
}

func (s *Server) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

func (s *Server) peerMonitoring() {
	for {
		select {
		case isLeader := <-s.raft.LeaderCh():
			if !isLeader {
				break
			}

			fmt.Println("raft notified")
			for _, peer := range s.peers {
				if !s.IsLeader() {
					break
				}
				if s.joinCluster(peer.Name, peer.Address) != nil {
					fmt.Printf("failed to join %s, %s\n", peer.Name, peer.Address)
					continue
				}
			}
		}
	}
}

func (s *Server) Watch(stream pb.RaftMemberShip_WatchServer) error {
	if !s.IsLeader() {
		st := status.New(codes.PermissionDenied, "Permission defined to watch")
		resps, _ := st.WithDetails(
			&epb.ErrorInfo{
				Reason: "NOT_LEADER",
				Domain: "membership.raft",
				Metadata: map[string]string{
					"leader_addr": string(s.raft.Leader()),
				},
			},
		)
		return resps.Err()
	}

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return stream.Send(&pb.WatchResponse{
				Data: "done",
			})
		}

		if err != nil {
			return err
		}

		fmt.Printf("%s, %s\n", req.Name, req.Version)

		table := fsm.HostMembership{Ver: req.Version, Members: []fsm.HostMember{
			{
				Name:      req.Name,
				Desc:      req.Name,
				CreatedAt: time.Now(),
			},
		}}

		cmd, _ := json.Marshal(table)

		fmt.Printf("FSM data apply: %s\n", cmd)

		s.raft.Apply(cmd, time.Millisecond*100)
	}
}
