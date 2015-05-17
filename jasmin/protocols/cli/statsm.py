import pickle
from jasmin.protocols.cli.managers import Manager
from .usersm import UserExist
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

    def smppc(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def smppcs(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

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