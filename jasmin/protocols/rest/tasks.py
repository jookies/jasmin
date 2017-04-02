from celery import Celery, Task
from celery.task import task
from celery.utils.log import get_task_logger

logger = get_task_logger(__name__)
app = Celery(__name__)
app.config_from_object('jasmin.protocols.rest.config')


class JasminTask(Task):
    def on_failure(self, exc, task_id, args, kwargs, einfo):
        logger.error('Task [%s] failed: %s', task_id, exc)


@task(bind=True, base=JasminTask)
def http_batch(self):
    pass
