"""This is the jasmin-celery and jasmin-restapi configurations"""

import logging

# RESTAPI
old_api_uri = 'http://127.0.0.1:1401'
show_jasmin_version = True
auth_cache_seconds = 10
auth_cache_max_keys = 500

log_level = logging.getLevelName('INFO')
log_file = '/var/log/jasmin/restapi.log'
log_rotate = 'W6'
log_format = '%(asctime)s %(levelname)-8s %(process)d %(message)s'
log_date_format = '%Y-%m-%d %H:%M:%S'

# CELERY
broker_url = 'amqp://guest:guest@127.0.0.1:5672//'
result_backend = 'redis://:@127.0.0.1:6379/1'
task_serializer = 'json'
result_serializer = 'json'
accept_content = ['json']
timezone = 'UTC'
enable_utc = True
# Will push messages back to Jasmin's http api with a maximum throughput *per worker*
# Please consider this is a *per worker* throughput !
# Set to zero (0) to disable throughput control
http_throughput_per_worker = 8
# When set to True, the batch executor will consider Jasmin's response time to
# control the batch throughput, slower response time will slow down the throughput
# and vice-versa
smart_qos = True
