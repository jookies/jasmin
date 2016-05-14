#!/bin/bash
set -e
rm -rf /tmp/*
if [ "$2" = "--enable-interceptor-client" ]; then
  echo 'Starting interceptord'
  interceptord.py &
fi
echo 'Starting jasmind'
exec "$@"

