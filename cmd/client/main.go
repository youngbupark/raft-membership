package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	pb "github.com/youngbupark/raft-membership/pkg/proto"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	name       = flag.String("name", "membersip", "membership")
	serverAddr = flag.String("server_addr", "localhost:10000", "The server address in the format of host:port")
)

func newWatchStream(addr string) (*grpc.ClientConn, pb.RaftMemberShip_WatchClient) {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())

	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	if err != nil {
		log.Printf("fail to dial: %v\n", err)
		return nil, nil
	}

	client := pb.NewRaftMemberShipClient(conn)

	stream, err := client.Watch(context.Background())
	if err != nil {
		log.Printf("%v.Watch(_) = _, %v\n", client, err)
		conn.Close()
		return nil, nil
	}

	return conn, stream
}

func runMessageLoop(stream pb.RaftMemberShip_WatchClient) (string, error) {
	leaderAddr := ""
	var returnError error

	go func() {
		for {
			in, err := stream.Recv()
			returnError = err
			if err != nil {
				errStatus, ok := status.FromError(err)
				if !ok {
					log.Printf("Failed to send a note 1: %v \n", err)
					return
				}

				log.Printf("Failed to send a note 2: %v\n", err)

				switch errStatus.Code() {
				case codes.PermissionDenied:
					errStatus, _ := status.FromError(err)
					errInfo := errStatus.Details()[0].(*epb.ErrorInfo)
					leaderAddr = errInfo.Metadata["leader_addr"]
					returnError = nil
					stream.CloseSend()
					log.Printf("new leader elected:%s", leaderAddr)
				}
				return
			}

			log.Printf("Got response: %s", in.Data)
		}
	}()

	for i := 0; i <= 10000; i++ {
		req := &pb.WatchRequest{
			Version: "0",
			Name:    fmt.Sprintf("membership-%d", i),
		}
		fmt.Printf("Sending request - %v\n", req)
		err := stream.Send(req)
		if err != nil {
			returnError = err
			log.Printf("Failed to send a note 2: %v\n", err)
			break
		}
		time.Sleep(time.Second * 2)
	}

	return leaderAddr, returnError
}

func main() {
	flag.Parse()

	servers := strings.Split(*serverAddr, ",")
	currentServerIndex := 0
	for {
		log.Printf("\n\nTry to connect server: %s", servers[currentServerIndex])
		conn, stream := newWatchStream(servers[currentServerIndex])
		if stream != nil {
			leader, err := runMessageLoop(stream)
			if leader != "" || err != nil {
				currentServerIndex = (currentServerIndex + 1) % len(servers)
			}
			conn.Close()
		}
		time.Sleep(time.Second * 1)
	}
}
