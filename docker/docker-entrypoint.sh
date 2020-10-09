#!/bin/bash
set -e

if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi

echo 'Starting jasmind'
exec "$@"
