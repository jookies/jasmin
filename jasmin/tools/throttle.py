import threading

from jasmin.tools.singleton import Singleton


class Throttle(object):
    __metaclass__ = Singleton

    def __init__(self):
        self.ttl = 60.0
        self.throttle = False

    def on(self):
        self.throttle = True
        t = threading.Timer(self.ttl, self._off)
        t.start()

    def _off(self):
        self.throttle = False

    def is_on(self):
        return self.throttle


throttle = Throttle()

