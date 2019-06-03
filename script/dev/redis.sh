#!/bin/sh

docker run -d \
        --restart=always \
        --name redis \
        -p 9379:9379 \
        -v $(pwd)/store/redis:/data \
        redis:latest \
        redis-server --appendonly yes