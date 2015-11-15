#!/bin/bash
set -e

/etc/init.d/rabbitmq-server start
/etc/init.d/redis-server start

if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
