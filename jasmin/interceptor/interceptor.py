import logging
from logging.handlers import TimedRotatingFileHandler
from twisted.spread import pb

LOG_CATEGORY = "jasmin-interceptor"

class InterceptorPB(pb.Avatar):
    def setConfig(self, InterceptorPBConfig):
        self.config = InterceptorPBConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, 
                when = self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False
        
        self.log.info('Interceptor configured and ready.')
        
    def setAvatar(self, avatar):
        if type(avatar) is str:
            self.log.info('Authenticated Avatar: %s' % avatar)
        else:
            self.log.info('Anonymous connection')
        
        self.avatar = avatar

    def perspective_run(self):
        return True