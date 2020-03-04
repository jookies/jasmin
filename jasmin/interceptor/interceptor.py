import cPickle as pickle
import datetime as dt
import logging
from logging.handlers import TimedRotatingFileHandler
from jasmin.tools.formatters import WhiteSpaceStrippingFormatter

from twisted.spread import pb

from jasmin.tools.eval import CompiledNode

LOG_CATEGORY = "jasmin-interceptor-pb"


class InterceptorPB(pb.Avatar):
    def __init__(self, InterceptorPBConfig):
        self.config = InterceptorPBConfig
        self.avatar = None

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, when=self.config.log_rotate)
            formatter = WhiteSpaceStrippingFormatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        self.log.info('Interceptor configured and ready.')

    def setAvatar(self, avatar):
        if isinstance(avatar, str):
            self.log.info('Authenticated Avatar: %s', avatar)
        else:
            self.log.info('Anonymous connection')

        self.avatar = avatar

    def perspective_run_script(self, pyCode, routable):
        """Will execute pyCode with the routable argument"""
        routable = pickle.loads(routable)
        smpp_status = http_status = None

        try:
            self.log.info('Running with a %s (from:%s, to:%s).',
                          routable.pdu.id,
                          routable.pdu.params['source_addr'],
                          routable.pdu.params['destination_addr'])
            self.log.debug('Running [%s]', pyCode)
            self.log.debug('... having routable with pdu: %s', routable.pdu)
            node = CompiledNode().get(pyCode)
            glo = {'routable': routable, 'smpp_status': smpp_status, 'http_status': http_status, 'extra': {}}

            # Run script and measure execution time
            start = dt.datetime.now()
            eval(node, {}, glo)
            end = dt.datetime.now()
            delay = (end - start).seconds
            self.log.debug('... took %s seconds.', delay)
        except Exception as e:
            self.log.error('Executing script on routable (from:%s, to:%s) returned: %s',
                           routable.pdu.params['source_addr'],
                           routable.pdu.params['destination_addr'],
                           '%s: %s' % (type(e), e))
            return False
        else:
            if 0 <= self.config.log_slow_script <= delay:
                self.log.warn('Execution delay [%ss] for script [%s].', delay, pyCode)

            if glo['smpp_status'] is None and glo['http_status'] is None:
                return pickle.dumps(glo['routable'], pickle.HIGHEST_PROTOCOL)
            else:
                # If we have one of the statuses set to non-zero value
                #  then both of them must be non-zero to avoid misbehaviour
                #  of differents apis: if we return an error in smpp, we must
                #  do the same in http as well.
                if glo['smpp_status'] is None or not isinstance(glo['smpp_status'], int):
                    # ESME_RUNKNOWNERR
                    self.log.info(
                        'Setting smpp_status to 255 when having http_status = %s and smpp_status = %s.',
                        glo['http_status'],
                        glo['smpp_status'])
                    glo['smpp_status'] = 255
                elif glo['http_status'] is None or not isinstance(glo['http_status'], int):
                    # Unknown Error
                    self.log.info(
                        'Setting http_status to 520 when having smpp_status = %s and http_status = %s.',
                        glo['smpp_status'],
                        glo['http_status'])
                    glo['http_status'] = 520

                r = {'http_status': glo['http_status'], 'smpp_status': glo['smpp_status'], 'extra': glo['extra']}
                self.log.info('Returning statuses: %s', r)
                return r
