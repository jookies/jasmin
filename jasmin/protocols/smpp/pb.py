import cPickle as pickle
import logging
from logging.handlers import TimedRotatingFileHandler

from twisted.internet import defer
from twisted.spread import pb

import jasmin

LOG_CATEGORY = "jasmin-smpps-pb"


class SMPPServerPB(pb.Avatar):
    def __init__(self, SmppServerPBConfig):
        self.config = SmppServerPBConfig
        self.avatar = None
        self.smpps = None

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
        if self.smpps is None:
            self.log.info('Added SMPP Server: %s', smppsFactory.config.id)
        else:
            self.log.info('Replaced SMPP Server: %s', smppsFactory.config.id)

        self.smpps = smppsFactory

    def perspective_list_bound_systemids(self):
        """Returning list of bound smpp systemd_ids"""

        systemdids = []
        for bound_connection in self.smpps.bound_connections:
            systemdids.append(bound_connection)

        return systemdids

    @defer.inlineCallbacks
    def perspective_deliverer_send_request(self, system_id, pdu, pickled=True):
        """Will lookup for a deliverer (for system_id) and call sendRequest on it"""

        if system_id in self.smpps.bound_connections:
            deliverer = self.smpps.bound_connections[system_id].getNextBindingForDelivery()
        else:
            deliverer = None

        # There were no deliverers !
        if deliverer is None:
            self.log.error('Found no deliverer on system_id %s', system_id)
            defer.returnValue(False)
        else:
            if pickled:
                pdu = pickle.loads(pdu)

            try:
                # Push pdu through the deliverer
                yield deliverer.sendRequest(pdu, deliverer.config().responseTimerSecs)
            except Exception as e:
                self.log.error('Caught an error while trying to push pdu through deliverer (system_id:%s): (%s) %s',
                               system_id, e.__class__.__name__, e)
                defer.returnValue(False)
            else:
                defer.returnValue(True)

    def perspective_version_release(self):
        return jasmin.get_release()

    def perspective_version(self):
        return jasmin.get_version()
