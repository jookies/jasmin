# -*- coding: utf-8 -*- 
# Copyright 2012 Fourat Zouari <fourat@gmail.com>
# See LICENSE for details.

import random
import urllib
from twisted.web.client import getPage
from twisted.internet import defer
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import HappySMSCTestCase, SubmitSmTestCaseTools
from jasmin.routing.test.codepages import (GSM0338, IA5_ASCII, ISO8859_1, 
                                           CYRILLIC, ISO_8859_8)
from jasmin.vendor.messaging.sms.gsm0338 import encode, decode

class SubmitSmCodingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    _long_extension = '.................................................................................................................................................................................'
    _long_gsm0338 = u'Simple and long !%s' % _long_extension
    _francais = u'tést français'                             # (utf8 / 1 byte coding)
    _long_francais = u'tést français %s' % _long_extension   # (utf8 / 1 byte coding)
    _gsm0338_ar = u'français عربي'                            # (utf16 / 2 byte coding)
    _long_gsm0338_ar = u'français عربي %s' % _long_extension  # (utf16 / 2 byte coding)
    _kosme = u'κόσμε'                                        # (utf16 / 2 byte coding)
    _rabbit = u'أرنب'                                        # (utf16 / 2 byte coding)
    _long_rabbit = u'أرنب %s' % _long_extension              # (utf16 / 2 byte coding)
    
    @defer.inlineCallbacks
    def run_test(self, content, datacoding = None):        
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        # Set content
        self.params['content'] = content
        # Set datacoding
        if datacoding is None and 'datacoding' in self.params:
            del self.params['datacoding']
        if datacoding is not None:
            self.params['datacoding'] = datacoding
        # Prepare baseurl
        baseurl = 'http://127.0.0.1:1401/send?%s' % urllib.urlencode(self.params)
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        
        yield self.stopSmppConnectors()
        
        # Run tests
        self.assertEqual(msgStatus, 'Success')
        if datacoding is None:
            datacoding = 0
        datacoding_matrix = {}
        datacoding_matrix[0] = {'schemeData': 'SMSC_DEFAULT_ALPHABET'}
        datacoding_matrix[1] = {'schemeData': 'IA5_ASCII'}
        datacoding_matrix[2] = {'schemeData': 'OCTET_UNSPECIFIED'}
        datacoding_matrix[3] = {'schemeData': 'LATIN_1'}
        datacoding_matrix[4] = {'schemeData': 'OCTET_UNSPECIFIED_COMMON'}
        datacoding_matrix[5] = {'schemeData': 'JIS'}
        datacoding_matrix[6] = {'schemeData': 'CYRILLIC'}
        datacoding_matrix[7] = {'schemeData': 'ISO_8859_8'}
        datacoding_matrix[8] = {'schemeData': 'UCS2'}
        datacoding_matrix[9] = {'schemeData': 'PICTOGRAM'}
        datacoding_matrix[10] = {'schemeData': 'ISO_2022_JP'}
        datacoding_matrix[13] = {'schemeData': 'EXTENDED_KANJI_JIS'}
        datacoding_matrix[14] = {'schemeData': 'KS_C_5601'}

        # Check for content encoding
        receivedContent = ':'.join(['%x' % ord(c) for c in self.SMSCPort.factory.lastClient.lastSubmitSmPDU.params['short_message']])
        sentContent = ':'.join(['%x' % ord(c) for c in content])
        self.assertEqual(sentContent, receivedContent)

        # Check for schemeData
        receivedSchemeData = str(self.SMSCPort.factory.lastClient.lastSubmitSmPDU.params['data_coding'].schemeData)
        sentDataCoding = datacoding_matrix[datacoding]['schemeData']
        self.assertEqual(sentDataCoding, receivedSchemeData)
        
    @defer.inlineCallbacks
    def test_gsm0338(self):
        _gsm0338_str = ''.join(random.sample(GSM0338, 80)) + ''.join(random.sample(GSM0338, 80))
        yield self.run_test(content = _gsm0338_str)

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = ''.join(random.sample(IA5_ASCII, 80)) + ''.join(random.sample(IA5_ASCII, 80))
        yield self.run_test(content = _ia5ascii_str, datacoding = 1)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass
    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_LATIN_1(self):
        _latin1_str = ''.join(random.sample(ISO8859_1, 140))
        yield self.run_test(content = _latin1_str, datacoding = 3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass
    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = '\x8140\x96BA\xE062\xEAA2'
        yield self.run_test(content = ''.join(_jisx201_str), datacoding = 5)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        _cyrillic_str = ''.join(random.sample(CYRILLIC, 140))
        yield self.run_test(content = _cyrillic_str, datacoding = 6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        _iso8859_8_str = ''.join(random.sample(ISO_8859_8, 140))
        yield self.run_test(content = _iso8859_8_str, datacoding = 7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = '\x0623\x0631\x0646\x0628' # Arabic word of 'rabbit'
        yield self.run_test(content = ''.join(_rabbit_arabic), datacoding = 8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = '\x8B89\x8B90\x8BC9\xFC4B'
        yield self.run_test(content = _cp932_str, datacoding = 9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass
    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = '\x8B89\x8B90\x8BC9\xFC4B'
        yield self.run_test(content = _cp932_str, datacoding = 13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = '\x8141\xA496\xE1D7\xFDFE'
        yield self.run_test(content = _ks_c_5601_str, datacoding = 14)

    def test_long_gsm0338(self):
        pass
    test_long_gsm0338.skip = 'TODO'

    def test_long_latin1(self):
        pass
    test_long_latin1.skip = 'TODO'

    def test_long_ucs2(self):
        pass
    test_long_ucs2.skip = 'TODO'