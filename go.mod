module github.com/openweft/weft-client

go 1.25.0

require (
	github.com/grpc-transports/ssh v0.0.0-00010101000000-000000000000
	github.com/grpc-transports/wireguard v0.0.0-00010101000000-000000000000
	github.com/hashicorp/hcl/v2 v2.24.0
	github.com/openweft/weft-proto v0.0.0
	github.com/zclconf/go-cty v1.18.1
	golang.org/x/crypto v0.50.0
	golang.org/x/oauth2 v0.36.0
	google.golang.org/grpc v1.80.0
)

require (
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c // indirect
)

replace (
	github.com/grpc-transports/ssh => ../../grpc-transports/ssh
	github.com/grpc-transports/wireguard => ../../grpc-transports/wireguard
	github.com/openweft/weft-proto => ../weft-proto
)
