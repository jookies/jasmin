from jasmin.tools.singleton import Singleton
from jasmin.tools.stats import Stats

class HttpAPIStatistics(Stats):
	"HTTP API statistics holder"

	def __init__(self, api_id):
		self.api_id = api_id

		self.init()

	def init(self):
		self._stats = {
			'created_at': 0,
			'request_count': 0,
			'last_request_at': 0,
			'auth_error_count': 0,
			'route_error_count': 0,
			'throughput_error_count': 0,
			'charging_error_count': 0,
			'server_error_count': 0,
			'success_count': 0,
			'last_success_at': 0,
		}

class HttpAPIStatsCollector:
	"HTTP API statistics collection holder"
	__metaclass__ = Singleton
	apis = {}

	def get(self):
		"Return a HTTP API's stats object or instanciate a new one"
		api_id = 'main'
		if api_id not in self.apis:
			self.apis[api_id] = HttpAPIStatistics(api_id)
		
		return self.apis[api_id]