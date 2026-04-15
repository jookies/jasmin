#!/bin/bash
set -e

# Clean lock files
echo 'Cleaning lock files'
rm -f /tmp/*.lock

# If RestAPI http Mode, start Guicorn
if [ "$RESTAPI_HTTP_MODE" = 1 ]; then
  # start restapi
  exec gunicorn -b 0.0.0.0:8080 jasmin.protocols.rest:api --access-logfile /var/log/jasmin/rest-api.access.log --disable-redirect-access-to-syslog

# If Celery Worker is enabled, start Celery worker
elif [ "$RESTAPI_WORKER_MODE" = 1 ]; then
  echo 'Starting Celery worker'
  exec celery -A jasmin.protocols.rest.tasks worker -l INFO -c 4 --autoscale=10,3

# Else start jasmind
else
  if [ "$2" = "--enable-interceptor-client" ]; then
    echo 'Starting interceptord'
    interceptord.py &
  fi
  echo 'Starting jasmind'
  exec "$@"
fi
