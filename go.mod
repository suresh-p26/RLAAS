module github.com/rlaas-io/rlaas

go 1.25

toolchain go1.25.8

require github.com/redis/go-redis/v9 v9.6.3

require (
	github.com/alicebob/miniredis/v2 v2.37.0
	google.golang.org/grpc v1.68.0
	google.golang.org/protobuf v1.34.2
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
)
