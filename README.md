
## protobuf grpc gen
protoc -I pkg/proto/ pkg/proto/membership.proto --go_out=plugins=grpc:membership --go-grpc_out=plugins=grpc:membership

## Start server

cd cmd/server
go build
./server -name node0 -b -env prod -initial-clusters node0=:9090,node1=:9091,node2=:9099 -port 50051
./server -name node1 -env prod -initial-clusters node0=:9090,node1=:9091,node2=:9099 -port 50052
./server -name node2 -env prod -initial-clusters node0=:9090,node1=:9091,node2=:9099 -port 50053

## Start client

cd cmd/client
go run . -server_addr 127.0.0.1:50051,127.0.0.1:50052,127.0.0.1:50053

