from jasmin.tools.singleton import Singleton

class KeyNotFound(Exception):
    """
    Raised when setting or getting an unknown statistics key
    """

class KeyNotIncrementable(Exception):
    """
    Raised when trying to increment a non integer key
    """

class ConnectorStatistics:

	def set(self, key, value):
		if key not in self._stats:
			raise KeyNotFound(key)

		self._stats[key] = value

	def get(self, key):
		if key not in self._stats:
			raise KeyNotFound(key)

		return self._stats[key]

	def inc(self, key, inc = 1):
		if key not in self._stats:
			raise KeyNotFound(key)
		if type(self._stats[key]) != int:
			raise KeyNotIncrementable(key)

		self._stats[key]+= inc

class ClientConnectorStatistics(ConnectorStatistics):
	"One client connector statistics holder"

	def __init__(self, cid):
		self.cid = cid

		self._stats = {
			'created_at': 0,
			'last_received_pdu_at': 0,
			'last_sent_pdu_at': 0,
			'last_received_elink_at': 0,
			'last_sent_elink_at': 0,
			'last_seqNum_at': 0,
			'last_seqNum': None,
			'connected_at': 0,
			'bound_at': 0,
			'disconnected_at': 0,
			'connected_count': 0,
			'bound_count': 0,
			'disconnected_count': 0,
		}

class ServerConnectorStatistics(ConnectorStatistics):
	"One server connector statistics holder"

	def __init__(self, cid):
		self.cid = cid

		self._stats = {
			'created_at': 0,
			'last_received_pdu_at': 0,#TODO
			'last_sent_pdu_at': 0,#TODO
			'last_received_elink_at': 0,#TODO
			'last_sent_elink_at': 0,#TODO
			'last_seqNum_at': 0,#TODO
			'last_seqNum': None,#TODO
			'connected_at': 0,#TODO
			'bound_at': 0,#TODO
			'disconnected_at': 0,#TODO
			'connected_count': 0,#TODO
			'bound_count': 0,#TODO
			'disconnected_count': 0,#TODO
		}

class SMPPClientStatsCollector:
	"SMPP Clients statistics collection holder"
	__metaclass__ = Singleton
	connectors = {}

	def get(self, cid):
		"Return a connector's stats object or instanciate a new one"
		if cid not in self.connectors:
			self.connectors[cid] = ClientConnectorStatistics(cid)
		
		return self.connectors[cid]

class SMPPServerStatsCollector:
	"SMPP Servers statistics collection holder"
	__metaclass__ = Singleton
	connectors = {}

	def get(self, cid):
		"Return a connector's stats object or instanciate a new one"
		if cid not in self.connectors:
			self.connectors[cid] = ServerConnectorStatistics(cid)
		
		return self.connectors[cid]