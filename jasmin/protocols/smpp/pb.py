import logging
from logging.handlers import TimedRotatingFileHandler

from twisted.spread import pb

LOG_CATEGORY = "jasmin-smpps-pb"


class SMPPServerPB(pb.Avatar):
    def __init__(self, SmppServerPBConfig):
        self.config = SmppServerPBConfig
        self.avatar = None
        self.smpp_servers = {}

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        if len(self.log.handlers) != 1:
            self.log.setLevel(self.config.log_level)
            handler = TimedRotatingFileHandler(filename=self.config.log_file, when=self.config.log_rotate)
            formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
            handler.setFormatter(formatter)
            self.log.addHandler(handler)
            self.log.propagate = False

        self.log.info('SmppServerPB configured and ready.')

    def setAvatar(self, avatar):
        if isinstance(avatar, str):
            self.log.info('Authenticated Avatar: %s', avatar)
        else:
            self.log.info('Anonymous connection')

        self.avatar = avatar

    def addSmpps(self, smppsFactory):
        if smppsFactory.config.id not in self.smpp_servers:
            self.log.info('Added a new SMPP Server: %s', smppsFactory.config.id)
        else:
            self.log.info('Replaced SMPP Server: %s', smppsFactory.config.id)

        self.smpp_servers[smppsFactory.config.id] = smppsFactory

    def perspective_smpps_list(self):
        """Returning list of available smpp servers"""
