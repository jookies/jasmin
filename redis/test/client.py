# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

from twisted.internet import defer
from jasmin.redis.configs import RedisForJasminConfig
from jasmin.redis.client import ConnectionWithConfiguration

class RedisGenerickClient():
    """A simple client connecting with default configuration
    """
    
    def __init__(self):

        # Connect to redis server
        RedisForJasminConfigInstance = RedisForJasminConfig()
        # No auth
        RedisForJasminConfigInstance.password = None
        
        self.rc = ConnectionWithConfiguration(RedisForJasminConfigInstance)
        # Authenticate and select db
        if RedisForJasminConfigInstance.password is not None:
            self.rc.auth(RedisForJasminConfigInstance.password)
            self.rc.select(RedisForJasminConfigInstance.dbid)