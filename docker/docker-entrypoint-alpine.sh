#!/bin/bash
set -e

echo 'Starting RabbitMQ'
rc-service rabbitmq-server start
echo 'Starting Redis'
rc-service redis-server start
echo 'Starting supervisor'
rc-service supervisor start

echo 'Cleaning lock files'
rm -f /tmp/*.lock

if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
