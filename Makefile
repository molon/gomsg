PROJECT_ROOT := github.com/molon/gomsg
LOCALIP := $(shell ifconfig | grep -Eo 'inet (addr:)?([0-9]*\.){3}[0-9]*' | grep -Eo '([0-9]*\.){3}[0-9]*' | grep -v '127.0.0.1' | awk '{print $1}')

# configuration for building on host machine
GO := GO111MODULE=on go

# configuration for the protobuf gentool
SRCROOT_ON_HOST		:= $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
SRCROOT_IN_CONTAINER	:= /go/src/$(PROJECT_ROOT)
DOCKER_RUNNER    	:= docker run -u `id -u`:`id -g` --rm
DOCKER_RUNNER		+= -v $(SRCROOT_ON_HOST):$(SRCROOT_IN_CONTAINER)
DOCKER_GENERATOR	:= molon/protoc-gentool:latest
GENERATOR		:= $(DOCKER_RUNNER) $(DOCKER_GENERATOR)

# configuration env
ETCD_CONTAINER_NAME := gomsg-etcd
JAEGER_CONTAINER_NAME := gomsg-jaeger
REDIS_CONTAINER_NAME := gomsg-redis

# env
.PHONY: clean-envs
clean-envs:
	@docker stop $(ETCD_CONTAINER_NAME) || true
	@docker rm $(ETCD_CONTAINER_NAME) || true
	@docker stop $(REDIS_CONTAINER_NAME) || true
	@docker rm $(REDIS_CONTAINER_NAME) || true
	@docker stop $(JAEGER_CONTAINER_NAME) || true
	@docker rm $(JAEGER_CONTAINER_NAME) || true
	@rm -rf $(CURDIR)/store/

.PHONY: etcd
etcd:
	@docker stop $(ETCD_CONTAINER_NAME) || true
	@docker rm $(ETCD_CONTAINER_NAME) || true
	@docker run -d \
        --restart=always \
        --name $(ETCD_CONTAINER_NAME) \
        -p 8379:2379 \
        -p 8380:2380 \
        -v $(CURDIR)/store/etcd:/etcd-data \
        quay.io/coreos/etcd:latest \
        /usr/local/bin/etcd \
        --data-dir=/etcd-data --name node1 \
        --listen-peer-urls http://0.0.0.0:2380 \
        --listen-client-urls http://0.0.0.0:2379 \
        --initial-advertise-peer-urls http://${LOCALIP}:8380 \
        --advertise-client-urls http://${LOCALIP}:8379 \
        --initial-cluster node1=http://${LOCALIP}:8380

.PHONY: redis
redis:
	@docker stop $(REDIS_CONTAINER_NAME) || true
	@docker rm $(REDIS_CONTAINER_NAME) || true
	@docker run -d \
        --restart=always \
        --name $(REDIS_CONTAINER_NAME) \
        -p 9379:6379 \
        -v $(CURDIR)/store/redis:/data \
        redis:latest \
        redis-server --appendonly yes

.PHONY: jaeger
jaeger:
	@docker stop $(JAEGER_CONTAINER_NAME) || true
	@docker rm $(JAEGER_CONTAINER_NAME) || true
	@docker run -d \
		 --restart=always \
		 --name $(JAEGER_CONTAINER_NAME) \
		 -p 24268:14268 \
		 -p 6775:5775/udp -p 7831:6831/udp -p 7832:6832/udp -p 6778:5778 -p 26686:16686 -p 10411:9411 \
		 -e COLLECTOR_ZIPKIN_HTTP_PORT=9411 \
		 jaegertracing/all-in-one:latest
	
# protobuf gentool
.PHONY: internal_pb
internal_pb:
	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/internal/pb/boatpb/boat.proto

	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/internal/pb/stationpb/station.proto

	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/internal/pb/mqpb/mq.proto

.PHONY: pb
pb:
	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/pb/msgpb/msg.proto

	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	--grpc-gateway_out="logtostderr=true:." \
	$(PROJECT_ROOT)/pb/pushpb/push.proto

	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/pb/authpb/auth.proto

	@$(GENERATOR) \
	-I$(SRCROOT_IN_CONTAINER)/pb \
	--go_out=plugins=grpc:. \
	$(PROJECT_ROOT)/pb/errorpb/code.proto

# go
.PHONY: fmt tidy
fmt:
	@go fmt $(shell GO111MODULE=on go list ./... | grep -v vendor)
tidy:
	@$(GO) mod tidy

# build
.PHONY: build build_boat build_station build_carrier build_client build_auth
build: build_boat build_station build_carrier
build_boat: tidy
	@$(GO) build -o ./bin/boat ./cmd/boat/
build_station: tidy
	@$(GO) build -o ./bin/station ./cmd/station/
build_carrier: tidy
	@$(GO) build -o ./bin/carrier ./cmd/carrier/

build_client: tidy
	@$(GO) build -o ./bin/client ./client/
build_auth: tidy
	@$(GO) build -o ./bin/auth ./example/auth/

# run
.PHONY: boat station carrier client auth
boat: build_boat
	@./bin/boat
station: build_station
	@./bin/station
carrier: build_carrier
	@./bin/carrier

client: build_client
	@./bin/client
auth: build_auth
	@./bin/auth

.PHONY: all
all: build