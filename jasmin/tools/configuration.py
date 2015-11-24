import pickle
import re
import logging
from dateutil.parser import parse as date_parse

LOGGING_HANDLER = 'jcli'

_REGEX_VER = re.compile('^(?P<major>(\d+))'
                        '\.(?P<minor>(\d+))'
                        '([a-z.]*)'
                        '(?P<patch>(\d+))$')
_REGEX_HEADER = re.compile('^Persisted on (?P<date>(.*)) \[Jasmin (?P<release_version>(.*))\]')

class ConfigurationMigrator(object):
    "Responsible of migrating old saved configuration to recent definition, if any"

    def __init__(self, context, header, data):
        "Will contain inputs and parse header to get version and date of persisted data"
        self.log = logging.getLogger(LOGGING_HANDLER)
        self.context = context
        self.data = pickle.loads(data)
        self.log.debug('Initializing CM with context:%s, header:%s' % (self.context, header))

        # Parse header and get version & date
        match = _REGEX_HEADER.match(header)
        if match is None:
            raise ValueError('Invalid Jasmin configuration header format:' % header)
        self.date = date_parse(match.groupdict()['date'])
        self.str_version = match.groupdict()['release_version']

        # Parse version and convert it to float for comparaison
        match = _REGEX_VER.match(self.str_version)
        if match is None:
            raise ValueError('%s is not valid Jasmin version string' % self.str_version)

        self.version = float("%s.%s%s" % (match.groupdict()['major'],
                                          match.groupdict()['minor'], match.groupdict()['patch']))
        self.log.debug('[%s] @%s/%s:%s' % (self.context, self.date, self.str_version, self.version))

    def getMigratedData(self):
        "Return data after executing migration steps"
        return self.data
