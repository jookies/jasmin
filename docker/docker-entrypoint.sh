#!/bin/bash
set -e

echo 'Starting RabbitMQ'
/etc/init.d/rabbitmq-server start
echo 'Starting Redis'
/etc/init.d/redis-server start
echo 'Starting supervisor'
/etc/init.d/supervisor start

echo 'Cleaning lock files'
rm -f /tmp/*.lock

if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
