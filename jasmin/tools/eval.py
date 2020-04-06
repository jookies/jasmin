from jasmin.tools.singleton import Singleton


class CompiledNode:
    """A compiled code holder singleton"""
    __metaclass__ = Singleton
    nodes = {}

    def get(self, pyCode):
        """Return a compiled pyCode object or instanciate a new one"""
        pyCodeHash = hash(pyCode)
        if pyCodeHash not in self.nodes:
            self.nodes[pyCodeHash] = compile(pyCode, '', 'exec')

        return self.nodes[pyCodeHash]
