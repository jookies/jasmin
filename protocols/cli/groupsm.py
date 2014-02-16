from managers import Manager

class GroupsManager(Manager):
    managerName = 'group'
    
    def persist(self, arg, opts):
        # @todo
        raise NotImplementedError
        
    def load(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def list(self, arg, opts):
        # @todo
        # @todo List users of a given group (optionnal arg)
        raise NotImplementedError
    
    def add(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def update(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def remove(self, arg, opts):
        # @todo
        raise NotImplementedError
    
    def show(self, arg, opts):
        # @todo
        raise NotImplementedError