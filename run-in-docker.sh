#!/bin/sh

docker build -t billyzhang2010/scope-network-control:latest .
docker push  billyzhang2010/scope-network-control:latest
docker run --rm -it --net=host --pid=host --privileged -v /var/run:/var/run --name billyzhang2010-scope-network-control billyzhang2010/scope-network-control:latest
