import time

import requests
from celery import Celery, Task
from celery.task import task
from datetime import datetime, timedelta

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
    def __init__(self):
        super(JasminTask, self).__init__()

        # Shared namespace
        self.worker_tracker = {'last_req_at': datetime.now(), 'last_req_time': 0, 'throughput': 0}

    def on_failure(self, exc, task_id, args, kwargs, einfo):
        logger.error('Task [%s] failed: %s', task_id, exc)


@task(bind=True, base=JasminTask)
def httpapi_send(self, batch_id, batch_config, message_params, config):
    """Calls Jasmin's /send http api, if we have errback_url and callback_url in batch_config then
    will callback those urls asynchronously to inform user of batch progression"""
    try:
        slow_down_seconds = 0
        # Shall we do QoS control ?
        if self.worker_tracker['throughput'] > 0:
            qos_throughput_second = 1 / float(self.worker_tracker['throughput'])
            qos_throughput_ysecond_td = timedelta(microseconds=qos_throughput_second * 1000000)
            qos_delay = datetime.now() - self.worker_tracker['last_req_at']
            if qos_delay < qos_throughput_ysecond_td:
                slow_down_seconds = float((qos_throughput_ysecond_td - qos_delay).microseconds) / 1000000
                logger.debug('QoS: slowing down request by %s/s to meet configured throughput per worker: %s/s',
                             slow_down_seconds, self.worker_tracker['throughput'])

        # Shall we sleep ?
        if slow_down_seconds > 0:
            time.sleep(slow_down_seconds)

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
        # Useful for QoS control
        self.worker_tracker['last_req_at'] = datetime.now()

        # Smart throughput calculation
        if self.worker_tracker['throughput'] == 0 and config['throughput'] > 0:
            current_throughput = config['throughput']
        else:
            current_throughput = self.worker_tracker['throughput']
        if config['smart_qos'] and self.worker_tracker['last_req_time'] is not None:
            if r.elapsed.total_seconds() > self.worker_tracker['last_req_time']:
                # We have a slower request, we need to slow down the throughput
                if current_throughput > 0 and (current_throughput - (current_throughput * 10 / 100.0)) > 0:
                    logger.debug('Smart QoS: Slowing down throughput %s/s to -10%%', current_throughput)
                    current_throughput = current_throughput - (current_throughput * 10 / 100.0)
                elif current_throughput == 0:
                    logger.debug('Smart QoS: Slowing down throughput %s/s to fixed 0.5/s', current_throughput)
                    current_throughput = 0.5
                    # Else: keep current_throughput as is since it cannot go down to zero
            elif r.elapsed.total_seconds() < self.worker_tracker['last_req_time']:
                # We have a slower request, we need to boost the throughput
                if (current_throughput > 0 and config['throughput'] > 0 and (
                        current_throughput + (current_throughput * 10 / 100.0)) <= config['throughput']):
                    logger.debug('Smart QoS: Boosting throughput %s/s to +10%%', current_throughput)
                    current_throughput = current_throughput + (current_throughput * 10 / 100.0)
                elif current_throughput > 0 and config['throughput'] == 0:
                    logger.debug('Smart QoS: Restoring throughput %s/s to unlimited', current_throughput)
                    current_throughput = 0

        self.worker_tracker['throughput'] = current_throughput
        self.worker_tracker['last_req_time'] = r.elapsed.total_seconds()

        # Return status back
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
