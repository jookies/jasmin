from twisted.internet import defer
from datetime import datetime, timedelta
from jasmin.protocols.http.test.test_server import HTTPApiTestCases

class CnxStatusCases(HTTPApiTestCases):

	@defer.inlineCallbacks
	def test_connects_count(self):
		self.assertEqual(self.u1.CnxStatus.httpapi['connects_count'], 0)

		for i in range(100):
			yield self.web.get("send", {'username': self.u1.username, 
                                    	'password': 'correct',
                                    	'to': '98700177',
                                    	'content': 'anycontent'})

		self.assertEqual(self.u1.CnxStatus.httpapi['connects_count'], 100)

	@defer.inlineCallbacks
	def test_last_activity_at(self):
		self.assertEqual(self.u1.CnxStatus.httpapi['last_activity_at'], 0)

		yield self.web.get("send", {'username': self.u1.username, 
                                   	'password': 'correct',
                                   	'to': '98700177',
                                   	'content': 'anycontent'})

		self.assertApproximates(datetime.now(), 
								self.u1.CnxStatus.httpapi['last_activity_at'], 
								timedelta( seconds = 0.1 ))

	@defer.inlineCallbacks
	def test_submit_sm_request_count(self):
		self.assertEqual(self.u1.CnxStatus.httpapi['submit_sm_request_count'], 0)

		for i in range(100):
			yield self.web.get("send", {'username': self.u1.username, 
                                    	'password': 'correct',
                                    	'to': '98700177',
                                    	'content': 'anycontent'})

		self.assertEqual(self.u1.CnxStatus.httpapi['submit_sm_request_count'], 100)