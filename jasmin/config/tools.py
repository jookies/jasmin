"""
A Config file reader
"""

import ConfigParser

class ConfigFile(object):
    """
    Config file reader, will expose typed "ex: _getint()" and untyped "ex: _get()" with
    default fallback values to be returned if a configuration directive is not found or
    commented
    """

    def __init__(self, config_file=None):
        self.config_file = config_file

        # Parse config files and set options
        self.config = ConfigParser.RawConfigParser()
        if self.config_file is not None:
            self.config.read(config_file)

    def getConfigFile(self):
        'Return the current config_file'

        return self.config_file

    def _get(self, section, option, default=None):
        """
        Will check if section.option exists in config_file, return its value, default
        otherwise
        """

        if not self.config.has_section(section):
            return default
        if not self.config.has_option(section, option):
            return default
        if self.config.get(section, option) == 'None':
            return None

        return self.config.get(section, option)

    def _getint(self, section, option, default=None):
        """
        Will check if section.option exists in config_file, return its int casted value,
        default otherwise
        """

        if not self.config.has_section(section):
            return default
        if not self.config.has_option(section, option):
            return default
        if self.config.get(section, option) == 'None':
            return default

        return self.config.getint(section, option)

    def _getfloat(self, section, option, default=None):
        """
        Will check if section.option exists in config_file, return its float casted value,
        default otherwise
        """

        if not self.config.has_section(section):
            return default
        if not self.config.has_option(section, option):
            return default
        if self.config.get(section, option) == 'None':
            return default

        return self.config.getfloat(section, option)

    def _getbool(self, section, option, default=None):
        """
        Will check if section.option exists in config_file, return its bool casted value,
        default otherwise
        """

        if not self.config.has_section(section):
            return default
        if not self.config.has_option(section, option):
            return default

        return self.config.getboolean(section, option)
