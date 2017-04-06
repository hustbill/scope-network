FROM alpine:3.4
MAINTAINER Weaveworks Inc <help@weave.works>
LABEL works.weave.role=system
COPY ./network-control /usr/bin/network-control
RUN mkdir /lib64 && ln -s /lib/libc.musl-x86_64.so.1 /lib64/ld-linux-x86-64.so.2
RUN apk add --update iproute2 && rm -rf /var/cache/apk/*
ENTRYPOINT ["/usr/bin/network-control"]
