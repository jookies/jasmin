"""
Copyright 2009-2010 Mozes, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either expressed or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
"""

from datetime import datetime, tzinfo, timedelta
from jasmin.vendor.smpp.pdu.namedtuple import namedtuple

class FixedOffset(tzinfo):
    """Fixed offset in minutes east from UTC."""

    # Jasmin update, #267
    def __init__(self, offsetMin = 0, name = None):
        self.__offset = timedelta(minutes = offsetMin)
        self.__name = name

    def utcoffset(self, dt):
        return self.__offset

    def tzname(self, dt):
        return self.__name

    def dst(self, dt):
        return timedelta(0)

SMPPRelativeTime = namedtuple('SMPPRelativeTime', 'years, months, days, hours, minutes, seconds')

YYMMDDHHMMSS_FORMAT = "%y%m%d%H%M%S"

def parse_t(t_str):
    if len(t_str) != 1:
        raise ValueError("tenths of second must be one digit")
    return int(t_str)

def unparse_t(t):
    if t < 0 or t > 9:
        raise ValueError("tenths of second must be one digit")
    return '%1d' % t

def parse_nn(nn_str):
    if len(nn_str) != 2:
        raise ValueError("time difference must be two digits")
    nn = int(nn_str)
    if nn < 0 or nn > 48:
        raise ValueError("time difference must be 0-48")
    return nn
    
def unparse_nn(nn):
    if nn < 0 or nn > 48:
        raise ValueError("time difference must be 0-48")
    return '%02d' % nn

def parse_absolute_time(str):
    (YYMMDDhhmmss, t, nn, p) = (str[:12], str[12:13], str[13:15], str[15])
    
    if p not in ['+', '-']:
        raise ValueError("Invalid offset indicator %s" % p)
    
    tenthsOfSeconds = parse_t(t)
    quarterHrOffset = parse_nn(nn)
    
    microseconds = tenthsOfSeconds * 100 * 1000
    
    tzinfo = None
    if quarterHrOffset > 0:
        minOffset = quarterHrOffset * 15
        if p == '-':
            minOffset *= -1
        tzinfo = FixedOffset(minOffset, None)    
    
    timeVal = parse_YYMMDDhhmmss(YYMMDDhhmmss)
    return timeVal.replace(microsecond=microseconds,tzinfo=tzinfo)
    
def parse_relative_time(dtstr):
    # example 600 seconds is: '000000001000000R'

    try:
        year =  int(dtstr[:2])
        month = int(dtstr[2:4])
        day = int(dtstr[4:6])
        hour = int(dtstr[6:8])
        minute = int(dtstr[8:10])
        second = int(dtstr[10:12])
        dsecond = int(dtstr[12:13])

        # According to spec dsecond should be set to 0
        if dsecond != 0:
            raise ValueError("SMPP v3.4 spec violation: tenths of second value is %s instead of 0"% dsecond)
    except IndexError, e:
        raise ValueError("Error %s : Unable to parse relative Validity Period %s" % e,dtstr)

    return SMPPRelativeTime(year,month,day,hour,minute,second)
    
def parse_YYMMDDhhmmss(YYMMDDhhmmss):
    return datetime.strptime(YYMMDDhhmmss, YYMMDDHHMMSS_FORMAT)
    
def unparse_YYMMDDhhmmss(dt):
    return dt.strftime(YYMMDDHHMMSS_FORMAT)
    
def unparse_absolute_time(dt):
    if not isinstance(dt, datetime):
        raise ValueError("input must be a datetime but got %s" % type(dt))
    YYMMDDhhmmss = unparse_YYMMDDhhmmss(dt)
    tenthsOfSeconds = dt.microsecond/(100*1000)
    quarterHrOffset = 0
    p = '+'
    if dt.tzinfo is not None:
        utcOffset = dt.tzinfo.utcoffset(datetime.now())
        utcOffsetSecs = utcOffset.days * 60 * 60 * 24 + utcOffset.seconds
        quarterHrOffset =  utcOffsetSecs / (15*60)
        if quarterHrOffset < 0:
            p = '-'
            quarterHrOffset *= -1
    return YYMMDDhhmmss + unparse_t(tenthsOfSeconds) + unparse_nn(quarterHrOffset) + p

def unparse_relative_time(rel):
    if not isinstance(rel, SMPPRelativeTime):
        raise ValueError("input must be a SMPPRelativeTime")
    relstr = "%s%s%s%s%s%s000R" % (str("%.2d" % rel.years), str("%.2d" % rel.months), str("%.2d" % rel.days), str("%.2d" % rel.hours), str("%.2d" % rel.minutes), str("%.2d" % rel.seconds))

    return relstr

def parse(str):
    """Takes an SMPP time string in.
    Returns datetime.datetime for absolute time format
    Returns SMPPRelativeTime for relative time format (note: datetime.timedelta cannot
    because the SMPP relative time interval depends on the SMSC current date/time) 
    """
    if len(str) != 16:
        raise ValueError("Invalid time length %d" % len(str))
    if (str[-1]) == 'R':
        return parse_relative_time(str)
    return parse_absolute_time(str)
    
def unparse(dt_or_rel):
    """Takes in either a datetime or an SMPPRelativeTime
    Returns an SMPP time string
    """
    if isinstance(dt_or_rel, SMPPRelativeTime):
        return unparse_relative_time(dt_or_rel)
    return unparse_absolute_time(dt_or_rel)
    
    

