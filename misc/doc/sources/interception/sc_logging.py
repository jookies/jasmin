"This is how logging is done inside interception script"

import logging

# Set logger
logger = logging.getLogger('logging-example')
if len(logger.handlers) != 1:
    hdlr = logging.FileHandler('/var/log/jasmin/some_file.log')
    formatter = logging.Formatter('%(asctime)s %(levelname)s %(message)s')
    hdlr.setFormatter(formatter)
    logger.addHandler(hdlr)
    logger.setLevel(logging.DEBUG)

logger.info('Got pdu: %s' % routable.pdu)
