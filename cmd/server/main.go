package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/youngbupark/raft-membership/pkg/membership"
	pb "github.com/youngbupark/raft-membership/pkg/proto"
	"google.golang.org/grpc"
)

var (
	nodeName  = flag.String("name", "", "name of node")
	bootstrap = flag.Bool("b", false, "bootstrap node")
	clusters  = flag.String("initial-clusters", "", "cluster addresses")
	env       = flag.String("env", "dev", "environment")
	grpcPort  = flag.Int("port", -1, "gRPC endpoint port")
)

func parseCluster(cluster string) []membership.PeerInfo {
	var peers []membership.PeerInfo
	p := strings.Split(cluster, ",")
	for _, addr := range p {
		peer := strings.Split(addr, "=")
		if len(peer) != 2 {
			continue
		}
		peers = append(peers, membership.PeerInfo{
			Name:    peer[0],
			Address: peer[1],
		})
	}

	return peers
}

func startGRPCServer(srv pb.RaftMemberShipServer) {
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", *grpcPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)
	pb.RegisterRaftMemberShipServer(grpcServer, srv)
	grpcServer.Serve(lis)
}

func main() {
	flag.Parse()

	if *clusters == "" {
		panic("no clusters")
	}

	if *grpcPort == -1 {
		panic("port is required.")
	}

	if *nodeName == "" {
		panic("name is required")
	}

	peers := strings.Split(*clusters, ",")
	for i, addr := range peers {
		peers[i] = strings.TrimSpace(addr)
	}

	prs := parseCluster(*clusters)
	rAddress := ""
	for _, p := range prs {
		if p.Name == *nodeName {
			rAddress = p.Address
			break
		}
	}

	fmt.Printf("starting node ID: %s,%s \n\n", *nodeName, rAddress)

	server := membership.NewServer(*nodeName, rAddress, *env, prs, *bootstrap)
	if err := server.InitRaft(); err != nil {
		fmt.Printf("failed to init raft: %v", err)
	}

	go func() {
		for {
			select {
			case <-time.Tick(time.Second * 2):
				fmt.Printf("server loop - %s leader: %v \n", *nodeName, server.IsLeader())
				fmt.Printf("server loop - FSM state: %v \n", server.FSM().State().Members)
			}
		}
	}()

	startGRPCServer(server)

}
