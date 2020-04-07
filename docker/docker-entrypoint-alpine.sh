#!/bin/bash
set -e

# echo 'Starting RabbitMQ'
# rc-service /opt/rabbitmq/sbin/rabbitmq-server start

echo 'Cleaning lock files'
rm -f /tmp/*.lock

if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
