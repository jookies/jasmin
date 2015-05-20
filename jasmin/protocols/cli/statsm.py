import pickle
from jasmin.protocols.cli.managers import Manager
from jasmin.protocols.smpp.stats import SMPPClientStatsCollector
from .usersm import UserExist
from .smppccm import ConnectorExist
from tabulate import tabulate

def formatDateTime(dt):
    if dt == 0:
        return 'ND'
    else:
        return dt.strftime("%Y-%m-%d %H:%M:%S")

class StatsManager(Manager):
    managerName = 'stats'
    
    @UserExist(uid_key='user')
    def user(self, arg, opts):
        headers = ["#Item", "Type", "Value"]

        table = []

        user = self.pb['router'].getUser(opts.user)
        # SMPP Server stats
        for k,v in user.CnxStatus.smpps.iteritems():
            row = []
            row.append('#%s' % k)
            row.append('SMPP Server')
            if k[-3:] == '_at':
                row.append(formatDateTime(v))
            else:
                row.append(v)            

            table.append(row)

        # HTTP API stats
        for k,v in user.CnxStatus.httpapi.iteritems():
            row = []
            row.append('#%s' % k)
            row.append('HTTP Api')
            if k[-3:] == '_at':
                row.append(formatDateTime(v))
            else:
                row.append(v)            

            table.append(row)

        self.protocol.sendData(tabulate(table, headers, tablefmt = "plain", numalign = "left").encode('ascii'))

    def users(self, arg, opts):
        headers = ["#User id", "SMPP Bound connections", "SMPP L.A.", "HTTP requests counter", "HTTP L.A."]

        table = []
        users = pickle.loads(self.pb['router'].perspective_user_get_all(None))
        for user in users:
            row = []
            row.append('#%s' % user.uid)
            row.append(user.CnxStatus.smpps['bound_connections_count']['bind_receiver'] + 
                       user.CnxStatus.smpps['bound_connections_count']['bind_transmitter'] +
                       user.CnxStatus.smpps['bound_connections_count']['bind_transceiver']
            )
            row.append(formatDateTime(user.CnxStatus.smpps['last_activity_at']))
            row.append(user.CnxStatus.httpapi['connects_count'])
            row.append(formatDateTime(user.CnxStatus.httpapi['last_activity_at']))

            table.append(row)

        self.protocol.sendData(tabulate(table, headers, tablefmt = "plain", numalign = "left").encode('ascii'), prompt = False)
        self.protocol.sendData('Total users: %s' % (len(table)))

    @ConnectorExist(cid_key='smppc')
    def smppc(self, arg, opts):
        sc = SMPPClientStatsCollector()
        headers = ["#Item", "Value"]

        table = []
        for k, v in sc.get(opts.smppc)._stats.iteritems():
            row = []
            row.append('#%s' % k)
            if k[-3:] == '_at':
                row.append(formatDateTime(v))
            else:
                row.append(v)

            table.append(row)

        self.protocol.sendData(tabulate(table, headers, tablefmt = "plain", numalign = "left").encode('ascii'))

    def smppcs(self, arg, opts):
        sc = SMPPClientStatsCollector()
        headers = ["#Connector id", "Bound count", "Connected at", "Bound at", "Disconnected at", "Sent elink at", "Received elink at"]

        table = []
        connectors = self.pb['smppcm'].perspective_connector_list()
        for connector in connectors:
            row = []
            row.append('#%s' % connector['id'])
            row.append(sc.get(connector['id']).get('bound_count'))
            row.append(formatDateTime(sc.get(connector['id']).get('connected_at')))
            row.append(formatDateTime(sc.get(connector['id']).get('bound_at')))
            row.append(formatDateTime(sc.get(connector['id']).get('disconnected_at')))
            row.append(formatDateTime(sc.get(connector['id']).get('last_sent_elink_at')))
            row.append(formatDateTime(sc.get(connector['id']).get('last_received_elink_at')))

            table.append(row)

        self.protocol.sendData(tabulate(table, headers, tablefmt = "plain", numalign = "left").encode('ascii'), prompt = False)
        self.protocol.sendData('Total connectors: %s' % (len(table)))

    def moroute(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def moroutes(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def mtroute(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def mtroutes(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')
        
    def httpapi(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def smppsapi(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')