# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
from twisted.internet.protocol import ServerFactory
from jasmin.protocols.cli.jcli import JCliProtocol
from twisted.conch.telnet import TelnetTransport, TelnetBootstrapProtocol
from twisted.conch.insults import insults

LOG_CATEGORY_BASE="jcli"

class JCliFactory(ServerFactory):
    def __init__(self, config):
        self.config = config
        # Protocol sessions are kept here:
        self.sessions = {}
        self.sessionRef = 0
        self.sessionsOnline = 0
                
        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY_BASE)
        self.log.setLevel(config.log_level)
        handler = logging.FileHandler(filename=config.log_file)
        formatter = logging.Formatter(config.log_format, config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        # Init protocol
        self.protocol = lambda: TelnetTransport(TelnetBootstrapProtocol,
                               insults.ServerProtocol,
                               JCliProtocol,
                               factory = self,
                               log = self.log)