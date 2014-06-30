#! /bin/bash -ex

TMP=`mktemp -d`
export GOPATH=$TMP
go get github.com/DECK36/go-log2amqp
go build github.com/DECK36/go-log2amqp

cp $TMP/bin/go-log2amqp ./log2amqp

fpm -s dir -t deb --verbose \
	-n deck36-log2amqp --version 0.1 --iteration 1 \
	--url https://github.com/DECK36/go-log2amqp \
	--maintainer "Martin Schuette <martin.schuette@deck36.de>" \
	--prefix /usr/local/bin \
	--deb-default log2amqp.default \
	--deb-upstart log2amqp.upstart \
	log2amqp

