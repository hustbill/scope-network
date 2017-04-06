#!/usr/bin/env bash
#
# Update vendored dedendencies.
#
set -e

if ! [[ "$PWD" = "$GOPATH/src/github.com/hustbill/scope-network" ]]; then
  echo "must be run from \$GOPATH/src/github.com/hustbill/scope-network"
  exit 255
fi

if [ ! $(command -v glide) ]; then
        echo "glide: command not found"
        exit 255
fi

if [ ! $(command -v glide-vc) ]; then
        echo "glide-vc: command not found"
        exit 255
fi

glide update --strip-vcs --strip-vendor --update-vendored --delete
glide-vc --only-code --no-tests --keep="**/*.json.in"
