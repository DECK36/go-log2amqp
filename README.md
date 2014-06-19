go-log2amqp
=============

A simple daemon that reads a file (tail -f style)
and sends every line to an AMQP exchange.

Uses https://github.com/ActiveState/tail and https://github.com/streadway/amqp

Intended for nginx access logs -- so it does some special character
encoding/escaping for that format (because `\xXX` is not valid JSON).

```
Usage of ./go-log2amqp:
  -exchange="logtest": Durable AMQP exchange name
  -exchange-type="fanout": Exchange type - direct|fanout|topic|x-custom
  -file="/var/log/syslog": filename to watch
  -key="nginxlog": AMQP routing key
  -uri="amqp://user:password@broker.example.com:5672/vhost": AMQP URI
```
