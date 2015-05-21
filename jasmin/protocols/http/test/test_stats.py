"""
Test cases for jasmin.protocols.http.stats module.
"""

from datetime import datetime
from twisted.trial.unittest import TestCase
from jasmin.protocols.http.stats import HttpAPIStatsCollector
from jasmin.tools.stats import KeyNotFound, KeyNotIncrementable

class BasicTestCases(TestCase):
	def test_stats(self):
		stats = HttpAPIStatsCollector().get()

		self.assertEqual(len(stats._stats), 10)
		self.assertTrue('created_at' in stats._stats)
		self.assertTrue('request_count' in stats._stats)
		self.assertTrue('last_request_at' in stats._stats)
		self.assertTrue('auth_error_count' in stats._stats)
		self.assertTrue('route_error_count' in stats._stats)
		self.assertTrue('throughput_error_count' in stats._stats)
		self.assertTrue('charging_error_count' in stats._stats)
		self.assertTrue('server_error_count' in stats._stats)
		self.assertTrue('success_count' in stats._stats)
		self.assertTrue('last_success_at' in stats._stats)

	def test_is_singleton(self):
		i1 = HttpAPIStatsCollector()
		i2 = HttpAPIStatsCollector()
		self.assertEqual(i1, i2)

		i1.get().set('auth_error_count', 100)

		self.assertEqual(i1.get().get('auth_error_count'),
						 i2.get().get('auth_error_count'),
						 )

	def test_stats_set(self):
		stats = HttpAPIStatsCollector().get()

		stats.set('success_count', 1000)
		self.assertEqual(stats.get('success_count'), 1000)

	def test_stats_inc(self):
		stats = HttpAPIStatsCollector().get()

		current = stats.get('request_count')
		stats.inc('request_count', 1)
		self.assertEqual(stats.get('request_count'), current+1)

		stats.inc('request_count', 5)
		self.assertEqual(stats.get('request_count'), current+6)

	def test_exceptions(self):
		stats = HttpAPIStatsCollector().get()
		self.assertRaises(KeyNotFound, stats.get, 'anything')
		self.assertRaises(KeyNotFound, stats.set, 'anything', 22)
		self.assertRaises(KeyNotFound, stats.inc, 'anything')

		stats.set('created_at', datetime.now())
		self.assertRaises(KeyNotIncrementable, stats.inc, 'created_at')