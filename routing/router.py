# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import logging
import pickle
from twisted.spread import pb
from jasmin.routing.RoutingTables import MORoutingTable, MTRoutingTable

LOG_CATEGORY = "jasmin-router"

class RouterPB(pb.Root):
    def setConfig(self, RouterPBConfig):
        self.config = RouterPBConfig

        # Set up a dedicated logger
        self.log = logging.getLogger(LOG_CATEGORY)
        self.log.setLevel(self.config.log_level)
        handler = logging.FileHandler(filename=self.config.log_file)
        formatter = logging.Formatter(self.config.log_format, self.config.log_date_format)
        handler.setFormatter(formatter)
        self.log.addHandler(handler)
        
        # Set pickleProtocol
        self.pickleProtocol = self.config.pickle_protocol
        
        # Init routing-related objects
        self.mo_routing_table = MORoutingTable()
        self.mt_routing_table = MTRoutingTable()
        self.users = []
        
        self.log.info('Router configured and ready.')
        
    def getMORoutingTable(self):
        return self.mo_routing_table
    def getMTRoutingTable(self):
        return self.mt_routing_table
    def authenticateUser(self, username, password, pickled = False):
        for _user in self.users:
            if _user.username == username and _user.password == password:
                if pickled:
                    return pickle.dumps(_user, self.pickleProtocol)
                else:
                    return _user
        
        return None
    
    def remote_user_add(self, user):
        user = pickle.loads(user)
        self.log.debug('Adding a User: %s' % user)
        self.log.info('Adding a User (id:%s)' % user.uid)

        # Replace existant users
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.users.remove(_user)
                break 

        return self.users.append(user)
    
    def remote_user_authenticate(self, username, password):
        self.log.debug('Authenticating with username:%s and password:%s' % (username, password))
        self.log.info('Authentication request with username:%s' % username)

        return self.authenticateUser(username, password, True)
    
    def remote_user_remove(self, user):
        user = pickle.loads(user)
        self.log.debug('Removing a User: %s' % user)
        self.log.info('Removing a User (id:%s)' % user.uid)

        # Remove user
        for _user in self.users:
            if user.uid == _user.uid or user.username == _user.username:
                self.users.remove(_user)
                break 

    def remote_user_remove_all(self):
        self.log.info('Removing all users')
        
        self.users = []
        
        return True

    def remote_user_get_all(self):
        self.log.info('Getting all users')
        self.log.debug('Getting all users: %s' % self.users)

        return pickle.dumps(self.users)
    
    def remote_mtroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MT Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MT Route with order %s', order)

        return self.mt_routing_table.add(route, order)
    
    def remote_moroute_add(self, route, order):
        route = pickle.loads(route)
        self.log.debug('Adding a MO Route, order = %s, route = %s' % (order, route))
        self.log.info('Adding a MO Route with order %s', order)

        return self.mo_routing_table.add(route, order)
    
    def remote_mtroute_flush(self):
        self.log.info('Flushing MT Routing table')

        return self.mt_routing_table.flush()
    
    def remote_moroute_flush(self):
        self.log.info('Flushing MO Routing table')

        return self.mo_routing_table.flush()
    
    def remote_mtroute_get_all(self):
        self.log.info('Getting MT Routing table')
        
        routes = self.mt_routing_table.getAll()
        self.log.debug('Getting MT Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)
    
    def remote_moroute_get_all(self):
        self.log.info('Getting MO Routing table')

        routes = self.mo_routing_table.getAll()
        self.log.debug('Getting MO Routing table: %s', routes)

        return pickle.dumps(routes, self.pickleProtocol)