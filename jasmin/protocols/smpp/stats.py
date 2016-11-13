from jasmin.tools.singleton import Singleton
from jasmin.tools.stats import Stats


class ConnectorStatistics(Stats):

    def __init__(self, cid):
        self.cid = cid

        self.init()


class ClientConnectorStatistics(ConnectorStatistics):
    """One client connector statistics holder"""

    def init(self):
        self._stats = {
            "created_at": 0,
            "last_received_pdu_at": 0,
            "last_sent_pdu_at": 0,
            "last_received_elink_at": 0,
            "last_sent_elink_at": 0,
            "last_seqNum_at": 0,
            "last_seqNum": None,
            "connected_at": 0,
            "bound_at": 0,
            "disconnected_at": 0,
            "connected_count": 0,
            "bound_count": 0,
            "disconnected_count": 0,
            "submit_sm_request_count": 0,
            "submit_sm_count": 0,
            "deliver_sm_count": 0,
            "data_sm_count": 0,
            "elink_count": 0,
            "throttling_error_count": 0,
            "other_submit_error_count": 0,
            "interceptor_error_count": 0,
            "interceptor_count": 0}

    def getStats(self):
        return self._stats


class ServerConnectorStatistics(ConnectorStatistics):
    """One server connector statistics holder"""

    def init(self):
        self._stats = {
            "created_at": 0,
            "last_received_pdu_at": 0,
            "last_sent_pdu_at": 0,
            "last_received_elink_at": 0,
            "connected_count": 0,
            "connect_count": 0,
            "disconnect_count": 0,
            "bound_trx_count": 0,
            "bound_rx_count": 0,
            "bound_tx_count": 0,
            "bind_trx_count": 0,
            "bind_rx_count": 0,
            "bind_tx_count": 0,
            "unbind_count": 0,
            "submit_sm_request_count": 0,
            "submit_sm_count": 0,
            "deliver_sm_count": 0,
            "data_sm_count": 0,
            "elink_count": 0,
            "throttling_error_count": 0,
            "other_submit_error_count": 0,
            "interceptor_error_count": 0,
            "interceptor_count": 0}

    def getStats(self):
        return self._stats


class SMPPClientStatsCollector(object):
    """SMPP Clients statistics collection holder"""
    __metaclass__ = Singleton
    connectors = {}

    def get(self, cid):
        """Return a connector's stats object or instanciate a new one"""
        if cid not in self.connectors:
            self.connectors[cid] = ClientConnectorStatistics(cid)

        return self.connectors[cid]


class SMPPServerStatsCollector(object):
    """SMPP Servers statistics collection holder"""
    __metaclass__ = Singleton
    connectors = {}

    def get(self, cid):
        """Return a connector's stats object or instanciate a new one"""
        if cid not in self.connectors:
            self.connectors[cid] = ServerConnectorStatistics(cid)

        return self.connectors[cid]
