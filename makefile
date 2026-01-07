build-relay:
	cd service/relay
	GOOS=linux GOARCH=amd64 go build -o bin/relay.linux main.go

build-host:
	cd service/host
	go build -o bin/host main.go

build-client:
	cd service/client
	 go build -o bin/client main.go

build-all: build-relay build-host build-client

scp-relay: build-relay
	scp bin/relay.linux root@8.218.218.201:~/