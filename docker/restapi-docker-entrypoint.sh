#!/bin/bash
set -e

# Change binding host:port for redis, and amqp
sed -i "/\[redis-client\]/,/host=/  s/host=.*/host=$REDIS_CLIENT_HOST/" /etc/jasmin/jasmin.cfg
sed -i "/\[redis-client\]/,/port=/  s/port=.*/port=$REDIS_CLIENT_PORT/" /etc/jasmin/jasmin.cfg
sed -i "/\[amqp-broker\]/,/host=/  s/host=.*/host=$AMQP_BROKER_HOST/" /etc/jasmin/jasmin.cfg
sed -i "/\[amqp-broker\]/,/port=/  s/port=.*/port=$AMQP_BROKER_PORT/" /etc/jasmin/jasmin.cfg

echo 'Cleaning lock files'
rm -f /tmp/*.lock

# RestAPI
if [ "$RESTAPI_MODE" = 1 ]; then
  # find jasmin installation directory
  jasminRoot=$(python -c "import jasmin as _; print(_.__path__[0])")
  # update jasmin-restAPI config
  sed -i "/# RESTAPI/,/old_api_uri/  s/old_api_uri.*/old_api_uri = 'http:\/\/$RESTAPI_OLD_HTTP_HOST:1401'/" ${jasminRoot}/protocols/rest/config.py 
  sed -i "/# CELERY/,/broker_url/  s/broker_url.*/broker_url = 'amqp:\/\/guest:guest@$AMQP_BROKER_HOST:$AMQP_BROKER_PORT\/\/'/" ${jasminRoot}/protocols/rest/config.py 
  sed -i "/# CELERY/,/result_backend/  s/result_backend.*/result_backend = 'redis:\/\/:@$REDIS_CLIENT_HOST:$REDIS_CLIENT_PORT\/1'/" ${jasminRoot}/protocols/rest/config.py 
  exec gunicorn -b 0.0.0.0:8080 jasmin.protocols.rest:api --access-logfile /var/log/jasmin/rest-api.access.log --disable-redirect-access-to-syslog
else
  if [ "$ENABLE_PUBLISH_SUBMIT_SM_RESP" = 1 ]; then
    # Enable publish_submit_sm_resp
    echo 'Enabling publish_submit_sm_resp'
    sed -i "s/.*publish_submit_sm_resp\s*=.*/publish_submit_sm_resp=True/g" /etc/jasmin/jasmin.cfg
  else
    # Disable publish_submit_sm_resp
    echo 'Disabling publish_submit_sm_resp'
    sed -i "s/.*publish_submit_sm_resp\s*=.*/publish_submit_sm_resp=False/g" /etc/jasmin/jasmin.cfg
  fi

  if [ "$2" = "--enable-interceptor-client" ]; then
    echo 'Starting interceptord'
    interceptord.py &
  fi

  echo 'Starting jasmind'
  exec "$@"
fi
