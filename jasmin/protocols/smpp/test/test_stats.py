"""
Test cases for jasmin.protocols.smpp.stats module.
"""

from datetime import datetime
from twisted.trial.unittest import TestCase
from jasmin.protocols.smpp.stats import SMPPClientStatsCollector, SMPPServerStatsCollector
from jasmin.tools.stats import KeyNotFound, KeyNotIncrementable

class SMPPClientStatsCollectorBasicTestCases(TestCase):
	def test_is_singleton(self):
		i1 = SMPPClientStatsCollector()
		i2 = SMPPClientStatsCollector()
		self.assertEqual(i1, i2)

		i1.get(cid = 'testing').set('last_seqNum', 100)

		self.assertEqual(i1.get(cid = 'testing').get('last_seqNum'),
						 i2.get(cid = 'testing').get('last_seqNum'),
						 )

	def test_get_will_create_connector(self):
		c = SMPPClientStatsCollector()
		self.assertTrue('test_get_will_create_connector' not in c.connectors)

		c.get(cid = 'test_get_will_create_connector')
		self.assertTrue('test_get_will_create_connector' in c.connectors)

	def test_get_will_reuse_existent_connector(self):
		c = SMPPClientStatsCollector()
		self.assertTrue('test_get_will_reuse_existent_connector' not in c.connectors)

		i1 = c.get(cid = 'test_get_will_reuse_existent_connector')
		i2 = c.get(cid = 'test_get_will_reuse_existent_connector')
		self.assertEqual(i1, i2)

	def test_stats(self):
		stats = SMPPClientStatsCollector().get(cid = 'test_stats')

		self.assertEqual(stats._stats, {'bound_at': 0,
			'bound_count': 0,
			'connected_at': 0,
			'connected_count': 0,
			'created_at': 0,
			'data_sm_count': 0,
			'deliver_sm_count': 0,
			'disconnected_at': 0,
			'disconnected_count': 0,
			'elink_count': 0,
			'interceptor_count': 0,
			'interceptor_error_count': 0,
			'last_received_elink_at': 0,
			'last_received_pdu_at': 0,
			'last_sent_elink_at': 0,
			'last_sent_pdu_at': 0,
			'last_seqNum': None,
			'last_seqNum_at': 0,
			'other_submit_error_count': 0,
			'submit_sm_count': 0,
			'submit_sm_request_count': 0,
			'throttling_error_count': 0,
 		})

	def test_stats_set(self):
		stats = SMPPClientStatsCollector().get(cid = 'test_stats_set')
		self.assertEqual(stats.get('last_seqNum'), None)

		stats.set('last_seqNum', 2)
		self.assertEqual(stats.get('last_seqNum'), 2)

	def test_stats_inc(self):
		stats = SMPPClientStatsCollector().get(cid = 'test_stats_inc')
		self.assertEqual(stats.get('bound_count'), 0)

		stats.inc('bound_count')
		self.assertEqual(stats.get('bound_count'), 1)

		stats.inc('bound_count', 5)
		self.assertEqual(stats.get('bound_count'), 6)

	def test_exceptions(self):
		stats = SMPPClientStatsCollector().get(cid = 'test_exceptions')
		self.assertRaises(KeyNotFound, stats.get, 'anything')
		self.assertRaises(KeyNotFound, stats.set, 'anything', 22)
		self.assertRaises(KeyNotFound, stats.inc, 'anything')

		stats.set('created_at', datetime.now())
		self.assertRaises(KeyNotIncrementable, stats.inc, 'created_at')

class SMPPServerStatsCollectorBasicTestCases(TestCase):
	def test_is_singleton(self):
		i1 = SMPPServerStatsCollector()
		i2 = SMPPServerStatsCollector()
		self.assertEqual(i1, i2)

		i1.get(cid = 'testing').set('bind_rx_count', 100)

		self.assertEqual(i1.get(cid = 'testing').get('bind_rx_count'),
						 i2.get(cid = 'testing').get('bind_rx_count'),
						 )

	def test_get_will_create_connector(self):
		c = SMPPServerStatsCollector()
		self.assertTrue('test_get_will_create_connector' not in c.connectors)

		c.get(cid = 'test_get_will_create_connector')
		self.assertTrue('test_get_will_create_connector' in c.connectors)

	def test_get_will_reuse_existent_connector(self):
		c = SMPPServerStatsCollector()
		self.assertTrue('test_get_will_reuse_existent_connector' not in c.connectors)

		i1 = c.get(cid = 'test_get_will_reuse_existent_connector')
		i2 = c.get(cid = 'test_get_will_reuse_existent_connector')
		self.assertEqual(i1, i2)

	def test_stats(self):
		stats = SMPPServerStatsCollector().get(cid = 'test_stats')

		self.assertEqual(stats._stats, {'bind_rx_count': 0,
			'bind_trx_count': 0,
			'bind_tx_count': 0,
			'bound_rx_count': 0,
			'bound_trx_count': 0,
			'bound_tx_count': 0,
			'connect_count': 0,
			'connected_count': 0,
			'created_at': 0,
			'data_sm_count': 0,
			'deliver_sm_count': 0,
			'disconnect_count': 0,
			'elink_count': 0,
			'last_received_elink_at': 0,
			'last_received_pdu_at': 0,
			'last_sent_pdu_at': 0,
			'other_submit_error_count': 0,
			'submit_sm_count': 0,
			'submit_sm_request_count': 0,
			'throttling_error_count': 0,
			'unbind_count': 0,
			'interceptor_count': 0,
			'interceptor_error_count': 0,
 		})

	def test_stats_set(self):
		stats = SMPPServerStatsCollector().get(cid = 'test_stats_set')
		self.assertEqual(stats.get('bind_rx_count'), 0)

		stats.set('bind_rx_count', 2)
		self.assertEqual(stats.get('bind_rx_count'), 2)

	def test_stats_inc(self):
		stats = SMPPServerStatsCollector().get(cid = 'test_stats_inc')
		self.assertEqual(stats.get('bound_tx_count'), 0)

		stats.inc('bound_tx_count')
		self.assertEqual(stats.get('bound_tx_count'), 1)

		stats.inc('bound_tx_count', 5)
		self.assertEqual(stats.get('bound_tx_count'), 6)

	def test_exceptions(self):
		stats = SMPPServerStatsCollector().get(cid = 'test_exceptions')
		self.assertRaises(KeyNotFound, stats.get, 'anything')
		self.assertRaises(KeyNotFound, stats.set, 'anything', 22)
		self.assertRaises(KeyNotFound, stats.inc, 'anything')

		stats.set('created_at', datetime.now())
		self.assertRaises(KeyNotIncrementable, stats.inc, 'created_at')
