import cPickle as pickle
import re
import logging
from dateutil.parser import parse as date_parse
import jasmin
from .migration import MAP

LOGGING_HANDLER = 'jcli'

_REGEX_VER = re.compile(r'^(?P<major>(\d+))'
                        r'\.(?P<minor>(\d+))'
                        r'([a-z.]*)'
                        r'(?P<patch>(\d+))$')
_REGEX_HEADER = re.compile(r'^Persisted on (?P<date>(.*)) \[Jasmin (?P<release_version>(.*))\]')


def version_parse(version):
    """Will parse Jasmin release version and return a float, ex: 0.8rc2 will return 0.8002"""
    match = _REGEX_VER.match(version)
    if match is None:
        raise ValueError('%s is not valid Jasmin version string' % version)

    return float("%s.%s%s" % (match.groupdict()['major'],
                              match.groupdict()['minor'], match.groupdict()['patch'].zfill(3)))


def version_is_valid(version, condition):
    """Will compare version with condition, example of condition: <=0.52"""
    version = version_parse(version)

    if condition[:2] in ('>=', '<=', '=='):
        condition_operation = condition[:2]
        condition_value = float(condition[2:])
    elif condition[:1] in ('>', '<'):
        condition_operation = condition[:1]
        condition_value = float(condition[1:])
    else:
        raise ValueError('Invalid condition (%s) while verifying validity with %s' % (condition, version))

    if condition_operation == '>=' and version >= condition_value:
        return True
    elif condition_operation == '<=' and version <= condition_value:
        return True
    elif condition_operation == '==' and version == condition_value:
        return True
    elif condition_operation == '>' and version > condition_value:
        return True
    elif condition_operation == '<' and version < condition_value:
        return True
    else:
        return False


class ConfigurationMigrator(object):
    """Responsible of migrating old saved configuration to recent definition, if any"""

    def __init__(self, context, header, data):
        "Will contain inputs and parse header to get version and date of persisted data"
        self.log = logging.getLogger(LOGGING_HANDLER)
        self.context = context
        self.data = pickle.loads(data)
        self.log.debug('Initializing CM with context:%s, header:%s', self.context, header)

        # Parse header and get version & date
        match = _REGEX_HEADER.match(header)
        if match is None:
            raise ValueError('Invalid Jasmin configuration header format:' % header)
        self.date = date_parse(match.groupdict()['date'])
        self.version = match.groupdict()['release_version']
        self.log.debug('[%s] @%s/%s', self.context, self.date, self.version)

    def getMigratedData(self):
        """Return data after executing migration steps"""
        for m in MAP:
            # Context verification
            if self.context not in m['contexts']:
                self.log.debug('%s is not in map conditions: %s', self.context, m['contexts'])
                continue

            # Validate conditions (with AND operator)
            valid = True
            for condition in m['conditions']:
                self.log.debug('Checking condition: %s with version %s', condition, self.version)
                if not version_is_valid(self.version, condition):
                    self.log.debug('Condition failed: %s with version %s', condition, self.version)
                    valid = False
                    break

            # We have matching context and valid conditions
            if valid:
                for operation in m['operations']:
                    self.log.info('Migrating old data [%s] from v%s to v%s by calling %s(data)',
                                  self.context, self.version, jasmin.get_release(), operation.func_name)
                    self.data = operation(self.data, context=self.context)
        return self.data
