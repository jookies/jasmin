from jasmin.vendor.smpp.twisted.protocol import SMPPServerProtocol
from jasmin.vendor.smpp.pdu import pdu_types

from zope.interface import Interface

from twisted.internet.protocol import ServerFactory
# Jasmin update: direct import of UsernamePassword instead of cred
from twisted.cred.credentials import UsernamePassword
from twisted.cred import error
from twisted.internet import defer

import logging
import collections


LOG_CATEGORY="smpp.twisted.server"

class IAuthenticatedSMPP(Interface):
    pass

# Jasmin update: using UsernamePassword instead of cred.credentials.UsernamePassword
class UsernameAndPasswordAndIP(UsernamePassword):
    def __init__(self, username, password, client_ip_address):
        self.username = username
        self.password = password
        self.client_ip_address = client_ip_address
    
class SMPPServerFactory(ServerFactory):

    protocol = SMPPServerProtocol

    def __init__(self, config, auth_portal):
        self.config = config
        self.log = logging.getLogger(LOG_CATEGORY)
        # A dict of protocol instances for each of the current connections,
        # indexed by system_id 
        self.bound_connections = {}
        self._auth_portal = auth_portal
    
    def getConfig(self):
        return self.config
     
    def getBoundConnectionCount(self, system_id):
        if self.bound_connections.has_key(system_id):
            return self.bound_connections[system_id].getMaxTransmitReceiveBindCount()
        else:
            return 0

    def getBoundConnectionCountsStr(self, system_id):
        if self.bound_connections.has_key(system_id):
            bind_counts = self.bound_connections[system_id].getBindingCountByType()
            bound_connections_count = []
            for key, value in bind_counts.iteritems(): 
                bound_connections_count.append("%s: %d" % (key, value))
            bound_connections_str = ', '.join(bound_connections_count)
            return bound_connections_str
        else:
            return '0'

    def addBoundConnection(self, connection):
        """
        Add a protocol instance to the list of current connections.
        @param connection: An instance of SMPPServerProtocol
        """
        system_id = connection.system_id
        self.log.debug('Adding SMPP binding for %s' % system_id)
        if not system_id in self.bound_connections:
            self.bound_connections[system_id] = SMPPBindManager(system_id)
        self.bound_connections[system_id].addBinding(connection)
        bind_type = connection.bind_type
        self.log.info("Added %s bind for '%s'. Active binds: %s. Max binds: %s" % (bind_type, system_id, self.getBoundConnectionCountsStr(system_id), self.config.systems[system_id]['max_bindings']))
        
    def removeConnection(self, connection):
        """
        Remove a protocol instance (SMPP binding) from the list of current connections.
        @param connection: An instance of SMPPServerProtocol
        """
        if connection.system_id is None:
            self.log.debug("SMPP connection attempt failed without binding.")
        else:
            system_id = connection.system_id
            bind_type = connection.bind_type
            self.bound_connections[system_id].removeBinding(connection)
            self.log.info("Dropped %s bind for '%s'. Active binds: %s. Max binds: %s" % (bind_type, system_id, self.getBoundConnectionCountsStr(system_id), self.config.systems[system_id]['max_bindings']))
            # If this is the last binding for this service then remove the BindManager
            if self.bound_connections[system_id].getBindingCount() == 0:
                self.bound_connections.pop(system_id)
        
    def getBoundConnections(self, system_id):
        return self.bound_connections.get(system_id)
    
    def login(self, system_id, password, client_ip_address):
        if self._auth_portal is not None:
            return self._auth_portal.login(
                UsernameAndPasswordAndIP(system_id, password, client_ip_address),
                None,
                IAuthenticatedSMPP
            )
        raise error.UnauthorizedLogin()
        
    def canOpenNewConnection(self, system_id, bind_type):
        """
        Checks if the gateway with the specified system_id can open a new
        connection, as it hasn't exceeded its maximum number of bindings.
        @param bind_type: One of smpp.pdu.pdu_types.CommandId
        """
        existing_bindings_for_id = self.getBoundConnections(system_id)
        if existing_bindings_for_id:
            connections_count = existing_bindings_for_id.getBindingCountForType(bind_type)
            return connections_count < self.config.systems[system_id]['max_bindings']
        else:
            # No existing bindings for this system_id
            return self.config.systems[system_id]['max_bindings'] > 0
        
    def unbindGateway(self, system_id):
        """ Unbinds and disconnects all the bindings for the given system_id.  """
        bind_mgr = self.getBoundConnections(system_id)
        if bind_mgr:
            unbinds_list = []
            for bind in bind_mgr:
                unbinds_list.append(bind.getDisconnectedDeferred())
                bind.unbindAndDisconnect()
            d = defer.DeferredList(unbinds_list)
        else:
            d = defer.succeed(None)
        
        return d

    def unbindAndRemoveGateway(self, system_id):
        '''
        Removes a running gateway from the config so they will be unable to rebind.
        Any attempt to bind while unbinding will receive a ESME_RBINDFAIL error.
        '''
        self.config.systems[system_id]['max_bindings'] = 0
        d = self.unbindGateway(system_id)
        d.addCallback(self.removeGatewayFromConfig, system_id)
        return d

    def removeGatewayFromConfig(self, deferred_res, system_id):
        self.config.systems.pop(system_id)
        return deferred_res

class SMPPBindManager(object):
    
    def __init__(self, system_id):
        self.system_id = system_id
        self._binds = {pdu_types.CommandId.bind_transceiver: [],
                       pdu_types.CommandId.bind_transmitter: [],
                       pdu_types.CommandId.bind_receiver: []}
        # A queue of the most recent bindings used for delivering messages
        self._delivery_binding_history = collections.deque()
        
    def addBinding(self, connection):
        """ @param connection: An instance of SMPPServerProtocol """
        
        bind_type = connection.bind_type
        self._binds[bind_type].append(connection)
        
    def removeBinding(self, connection):
        """ @param connection: An instance of SMPPServerProtocol """
        bind_type = connection.bind_type
        self._binds[bind_type].remove(connection)
        
    def getMaxTransmitReceiveBindCount(self):
        return len(self._binds[pdu_types.CommandId.bind_transceiver]) + \
               max(len(self._binds[pdu_types.CommandId.bind_transmitter]),
                   len(self._binds[pdu_types.CommandId.bind_receiver]))        
        
    def getBindingCount(self):       
        return sum(len(v) for v in self._binds.values())
    
    def getBindingCountByType(self):
        ret = {}
        for key, value in self._binds.iteritems():
            ret[key] = len(value)
        return ret

    def __len__(self):
        return self.getBindingCount()
    
    def __iter__(self):
        vals = []
        [vals.extend(type) for type in self._binds.values()]
        return vals.__iter__()
    
    def getBindingCountForType(self, bind_type):
        """
        Sum transceiver binds plus receiver or transmitter depending on this type
        @param bind_type: One of smpp.pdu.pdu_types.CommandId
        """
        if bind_type == pdu_types.CommandId.bind_transceiver:
            # Sum of current transceiver binds plus greater of current transmitter or receiver binds
            connections_count = self.getMaxTransmitReceiveBindCount()
        else:
            # Sum of transceiver binds plus existing binds of this type
            connections_count = sum([len(self._binds[bt]) for bt in (pdu_types.CommandId.bind_transceiver, bind_type)])
        return connections_count
    
    def getNextBindingForDelivery(self):
        """
        Messages inbound (MO) that are to be forwarded to
        the client systems can be sent via transceiver and
        receiver bindings. Call this method to determine which
        binding to send down next so that traffic travels equally
        down the different binds.
        @return smpp protocol or None
        """
        binding = None
        # If we now have more trx/rx bindings than have been used
        # then iterate through our trx/rx binds until we find one
        # that hasn't yet been used
        if len(self._delivery_binding_history) < self.getBindingCountForType(pdu_types.CommandId.bind_receiver):
            for binding in self._binds[pdu_types.CommandId.bind_receiver] + self._binds[pdu_types.CommandId.bind_transceiver]:
                if not binding in self._delivery_binding_history:
                    break
            else:
                binding = None
        
        # Otherwise send on the last trx/rx binding delivered on, as
        # long as it is still bound
        while binding is None and self._delivery_binding_history:
            # get last binding used
            _binding = self._delivery_binding_history.popleft()
            # check it is still bound
            if _binding in self._binds[_binding.bind_type]:
                # If so then use it
                binding = _binding
        
        if binding is not None:
            self._delivery_binding_history.append(binding)
        return binding
        
