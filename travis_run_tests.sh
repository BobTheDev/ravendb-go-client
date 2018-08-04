#!/bin/bash
set -u -e -o pipefail -o xtrace

#export RAVENDB_JAVA_TEST_SERVER_PATH=./RavenDB/Server/Raven.Server

export VERBOSE_LOG=true

go build -o ./capturer github.com/ravendb/ravendb-go-client/cmd/capture
# mark it as owend by root so that it has root priviledges even when
# not invoked by root.
# it needs root priviledges to capture packets
sudo chown root:wheel ./capturer
# set "follow user id on execution" bit so that it inherits root priviledges
# from file ownership
sudo chmod +s ./capturer

#go test -race -covermode=atomic -coverprofile=coverage.txt
go test -race -covermode=atomic -coverprofile=coverage.txt
