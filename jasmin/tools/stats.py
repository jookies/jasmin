class KeyNotFound(Exception):
    """
    Raised when setting or getting an unknown statistics key
    """

class KeyNotIncrementable(Exception):
    """
    Raised when trying to increment a non integer key
    """

class Stats:
    def set(self, key, value):
        if key not in self._stats:
            raise KeyNotFound(key)

        self._stats[key] = value

    def get(self, key):
        if key not in self._stats:
            raise KeyNotFound(key)

        return self._stats[key]

    def inc(self, key, inc = 1):
        if key not in self._stats:
            raise KeyNotFound(key)
        if type(self._stats[key]) != int:
            raise KeyNotIncrementable(key)

        self._stats[key]+= inc

    def dec(self, key, inc = 1):
        if key not in self._stats:
            raise KeyNotFound(key)
        if type(self._stats[key]) != int:
            raise KeyNotIncrementable(key)

        self._stats[key]-= inc