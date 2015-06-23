import logging

log = logging.getLogger("jasmin-router")

log.debug('Inside evalpy-test.py')
if routable.user.username == 'Evalpyusr2':
    log.info("Routable's username is Evalpyusr2 !")
    result = False
else:
    log.info("Routable's username is not Evalpyusr2: %s" % routable.user.username)
    result = True