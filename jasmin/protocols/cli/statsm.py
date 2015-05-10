from jasmin.protocols.cli.managers import Manager

class StatsManager(Manager):
    managerName = 'stats'
    
    def user(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

    def users(self, arg, opts):
        return self.protocol.sendData('Not implemented yet.')

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