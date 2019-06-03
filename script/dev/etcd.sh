#!/bin/sh

NODE1=192.168.31.241
        
docker run -d \
        --restart=always \
        --name etcd \
        -p 8379:2379 \
        -p 3380:2380 \
        -v $(pwd)/store/etcd:/etcd-data \
        quay.io/coreos/etcd:latest \
        /usr/local/bin/etcd \
        --data-dir=/etcd-data --name node1 \
        --listen-peer-urls http://0.0.0.0:2380 \
        --listen-client-urls http://0.0.0.0:2379 \
        --initial-advertise-peer-urls http://${NODE1}:3380 \
        --advertise-client-urls http://${NODE1}:8379 \
        --initial-cluster node1=http://${NODE1}:3380