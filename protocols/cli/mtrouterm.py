from managers import Manager

MTROUTES = ['DefaultRoute', 'StaticMTRoute', 'RandomRoundrobinMTRoute']

class MtRouterManager(Manager):
    managerName = 'mtrouter'
    
    def persist(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
        
    def load(self, arg, opts):
        print 'NotImplemented persist method in %s manager' % self.managerName
            
    def list(self, arg, opts):
        raise NotImplementedError
    
    def add(self, arg, opts):
        raise NotImplementedError
    
    def flush(self, arg, opts):
        raise NotImplementedError