#!/bin/bash
set -e

# Change binding host:port for redis, and amqp
sed -i "/\[redis-client\]/,/host=/  s/host=.*/host=$REDIS_CLIENT_HOST/" ${CONFIG_PATH}/jasmin.cfg
sed -i "/\[redis-client\]/,/port=/  s/port=.*/port=$REDIS_CLIENT_PORT/" ${CONFIG_PATH}/jasmin.cfg
sed -i "/\[amqp-broker\]/,/host=/  s/host=.*/host=$AMQP_BROKER_HOST/" ${CONFIG_PATH}/jasmin.cfg
sed -i "/\[amqp-broker\]/,/port=/  s/port=.*/port=$AMQP_BROKER_PORT/" ${CONFIG_PATH}/jasmin.cfg

echo 'Cleaning lock files'
rm -f /tmp/*.lock


if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
