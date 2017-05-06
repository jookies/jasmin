import requests
from celery import Celery, Task
from celery.task import task

from .config import *

# @TODO: make configuration loadable from /etc/jasmin/restapi.conf
logger = logging.getLogger('jasmin-restapi')
if len(logger.handlers) == 0:
    logger.setLevel(log_level)
    handler = logging.handlers.TimedRotatingFileHandler(filename=log_file, when=log_rotate)
    handler.setFormatter(logging.Formatter(log_format, log_date_format))
    logger.addHandler(handler)

app = Celery(__name__)
app.config_from_object('jasmin.protocols.rest.config')


class JasminTask(Task):
    def on_failure(self, exc, task_id, args, kwargs, einfo):
        logger.error('Task [%s] failed: %s', task_id, exc)


@task(bind=True, base=JasminTask)
def httpapi_send(self, batch_id, batch_config, message_params):
    """Calls Jasmin's /send http api, if we have errback_url and callback_url in batch_config then
    will callback those urls asynchronously to inform user of batch progression"""
    try:
        r = requests.get('%s/send' % old_api_uri, params=message_params)
    except requests.exceptions.ConnectionError as e:
        logger.error('[%s] Jasmin httpapi connection error: %s' % (batch_id, e))
        if batch_config.get('errback_url', None):
            batch_callback.delay(batch_config.get('errback_url'), batch_id, message_params['to'], 0,
                                 'HTTPAPI Connection error: %s' % e)
    except Exception as e:
        logger.error('[%s] Unknown error (%s): %s' % (batch_id, type(e), e))
        if batch_config.get('errback_url', None):
            batch_callback.delay(batch_config.get('errback_url'), batch_id, message_params['to'], 0,
                                 'Unknown error: %s' % e)
    else:
        if r.status_code != 200:
            logger.error('[%s] %s' % (batch_id, r.content.strip('"')))
            if batch_config.get('errback_url', None):
                batch_callback.delay(
                    batch_config.get('errback_url'), batch_id, message_params['to'], 0,
                    'HTTPAPI error: %s' % r.content.strip('"'))
        else:
            if batch_config.get('callback_url', None):
                batch_callback.delay(
                    batch_config.get('callback_url'), batch_id, message_params['to'], 1, r.content)


@task(bind=True, base=JasminTask)
def batch_callback(self, url, batch_id, to, status, status_text):
    try:
        if status == 0:
            operation_name = 'Errback'
        else:
            operation_name = 'Callback'

        requests.get(url, params={'batchId': batch_id, 'to': to, 'status': status, 'statusText': status_text})
    except Exception as e:
        logger.error('(%s) of batch %s to %s failed (%s): %s.' % (operation_name, batch_id, url, type(e), e))
    else:
        logger.info('(%s) of batch %s to %s succeeded.' % (operation_name, batch_id, url))
