import logging

# @TODO: make configuration loadable from /etc/jasmin/restapi.conf

# CELERY
broker_url = 'amqp://guest:guest@192.168.99.100:5672//'
result_backend = 'redis://:@192.168.99.100:6379/1'
task_serializer = 'json'
result_serializer = 'json'
accept_content = ['json']
timezone = 'UTC'
enable_utc = True

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
