"""This is an example of using EvalPyFilter with a database interrogation, it is written
for demonstration purpose only.
"""
import MySQLdb as mdb

destination_addr = routable.pdu.params['destination_addr']

try:
	con = mdb.connect('localhost', 'jasmin', 'somepassword', 'jasmin_faq');

	cur = con.cursor()
	cur.execute("SELECT COUNT(msisdn) FROM blacklisted_numbers WHERE msisdn = %s" % destination_addr)
	count = cur.fetchone()
	
	if count[0] == 0:
		# It is not blacklisted, filter will pass
		result = True
except mdb.Error, e:
	# A DB error, filter will block
	# Error can be logged as well ...
	result = False
finally:
	# Filter will block for any other exception / reason
	result = False