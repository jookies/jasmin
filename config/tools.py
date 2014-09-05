import ConfigParser

class ConfigFile:
    def __init__(self, config_file=None):
        self.config_file = config_file
        
        # Parse config files and set options
        self.config = ConfigParser.RawConfigParser()
        if self.config_file != None:
            self.config.read(config_file)
        
    def getConfigFile(self):
        return self.config_file
        
    def _get(self, section, option, default=None):
        if self.config.has_section(section) == False:
            return default
        if self.config.has_option(section, option) == False:
            return default
        
        return self.config.get(section, option)

    def _getint(self, section, option, default=None):
        if self.config.has_section(section) == False:
            return default
        if self.config.has_option(section, option) == False:
            return default
        
        return self.config.getint(section, option)
    
    def _getbool(self, section, option, default=None):
        if self.config.has_section(section) == False:
            return default
        if self.config.has_option(section, option) == False:
            return default
        
        return self.config.getboolean(section, option)
