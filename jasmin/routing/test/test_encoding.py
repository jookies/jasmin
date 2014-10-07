# -*- coding: utf-8 -*- 

import random
import urllib
from twisted.internet import reactor
from twisted.web.client import getPage
from twisted.web import server
from twisted.internet import defer
from jasmin.protocols.http.server import HTTPApi
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.routing.proxies import RouterPBProxy
from jasmin.routing.test.test_router import HappySMSCTestCase, SubmitSmTestCaseTools
from jasmin.routing.test.codepages import (GSM0338, IA5_ASCII, ISO8859_1, 
                                           CYRILLIC, ISO_8859_8)
from jasmin.vendor.messaging.sms.gsm0338 import encode, decode
from jasmin.vendor.smpp.pdu.pdu_types import EsmClassGsmFeatures

def composeMessage(characters, length):
    if length <= len(characters):
        return ''.join(random.sample(characters, length))
    else:
        s = ''
        while len(s) < length:
            s += ''.join(random.sample(characters, len(characters)))
        return s[:length]
    
class CodingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def run_test(self, content, datacoding = None, port = 1401):
        yield self.connect('127.0.0.1', self.pbPort)
        yield self.prepareRoutingsAndStartConnector()
        
        # Set content
        self.params['content'] = content
        # Set datacoding
        if datacoding is None and 'coding' in self.params:
            del self.params['coding']
        if datacoding is not None:
            self.params['coding'] = datacoding
        # Prepare baseurl
        baseurl = 'http://127.0.0.1:%s/send?%s' % (port, urllib.urlencode(self.params))
        
        # Send a MT
        # We should receive a msg id
        c = yield getPage(baseurl, method = self.method, postdata = self.postdata)
        msgStatus = c[:7]
        
        # Wait 2 seconds before stopping SmppClientConnectors
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

        yield self.stopSmppClientConnectors()
        
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
        receivedContent = ''
        for submitSm in self.SMSCPort.factory.lastClient.submitRecords:
            if EsmClassGsmFeatures.UDHI_INDICATOR_SET in submitSm.params['esm_class'].gsmFeatures and submitSm.params['short_message'][:3] == '\x05\x00\x03':
                receivedContent += submitSm.params['short_message'][6:]
            else:
                receivedContent += submitSm.params['short_message']
        self.assertEqual(content, receivedContent)

        # Check for schemeData
        sentDataCoding = datacoding_matrix[datacoding]['schemeData']
        for submitSm in self.SMSCPort.factory.lastClient.submitRecords:
            self.assertEqual(str(submitSm.params['data_coding'].schemeData), sentDataCoding)

class SubmitSmCodingTestCases(CodingTestCases):
    @defer.inlineCallbacks
    def test_gsm0338(self):
        _gsm0338_str = composeMessage(GSM0338, 160)
        yield self.run_test(content = _gsm0338_str)

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 160)
        yield self.run_test(content = _ia5ascii_str, datacoding = 1)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass
    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_LATIN_1(self):
        _latin1_str = composeMessage(ISO8859_1, 140)
        yield self.run_test(content = _latin1_str, datacoding = 3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass
    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeMessage({'\x8140', '\x96BA', '\xE062', '\xEAA2'}, 70)
        yield self.run_test(content = ''.join(_jisx201_str), datacoding = 5)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        _cyrillic_str = composeMessage(CYRILLIC, 140)
        yield self.run_test(content = _cyrillic_str, datacoding = 6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        _iso8859_8_str = composeMessage(ISO_8859_8, 140)
        yield self.run_test(content = _iso8859_8_str, datacoding = 7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeMessage({'\x0623', '\x0631', '\x0646', '\x0628'}, 70)
        yield self.run_test(content = ''.join(_rabbit_arabic), datacoding = 8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 70)
        yield self.run_test(content = _cp932_str, datacoding = 9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass
    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 70)
        yield self.run_test(content = _cp932_str, datacoding = 13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeMessage({'\x8141', '\xA496', '\xE1D7', '\xFDFE'}, 70)
        yield self.run_test(content = _ks_c_5601_str, datacoding = 14)

class LongSubmitSmCodingUsingSARTestCases(CodingTestCases):
    @defer.inlineCallbacks
    def test_gsm0338(self):
        _gsm0338_str = composeMessage(GSM0338, 612) # 612 = 153 * 4
        yield self.run_test(content = _gsm0338_str)

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 612) # 612 = 153 * 4
        yield self.run_test(content = _ia5ascii_str, datacoding = 1)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass
    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_LATIN_1(self):
        _latin1_str = composeMessage(ISO8859_1, 670) # 670 = 134 * 5
        yield self.run_test(content = _latin1_str, datacoding = 3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass
    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeMessage({'\x8140', '\x96BA', '\xE062', '\xEAA2'}, 335) # 335 = 67 * 5
        yield self.run_test(content = ''.join(_jisx201_str), datacoding = 5)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        _cyrillic_str = composeMessage(CYRILLIC, 670) # 670 = 134 * 5
        yield self.run_test(content = _cyrillic_str, datacoding = 6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        _iso8859_8_str = composeMessage(ISO_8859_8, 670) # 670 = 134 * 5
        yield self.run_test(content = _iso8859_8_str, datacoding = 7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeMessage({'\x0623', '\x0631', '\x0646', '\x0628'}, 335) # 335 = 67 * 5
        yield self.run_test(content = ''.join(_rabbit_arabic), datacoding = 8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _cp932_str, datacoding = 9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass
    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _cp932_str, datacoding = 13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeMessage({'\x8141', '\xA496', '\xE1D7', '\xFDFE'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _ks_c_5601_str, datacoding = 14)
        
class LongSubmitSmCodingUsingUDHTestCases(CodingTestCases):
    @defer.inlineCallbacks
    def setUp(self):
        yield CodingTestCases.setUp(self)
        
        # Start a new http server with long_content_split = 'udh'
        httpApiConfigInstance = HTTPApiConfig()
        httpApiConfigInstance.port = 1402
        httpApiConfigInstance.long_content_split = 'udh'

        # Launch the http server
        httpApi = HTTPApi(self.pbRoot_f, self.clientManager_f, httpApiConfigInstance)
        self.httpServer_udh = reactor.listenTCP(httpApiConfigInstance.port, server.Site(httpApi))
        self.httpPort_udh  = httpApiConfigInstance.port
        
    @defer.inlineCallbacks
    def tearDown(self):
        yield CodingTestCases.tearDown(self)
        self.httpServer_udh.stopListening()
    
    @defer.inlineCallbacks
    def test_gsm0338(self):
        _gsm0338_str = composeMessage(GSM0338, 612) # 612 = 153 * 4
        _gsm0338_str = composeMessage({'_'}, 612) # 612 = 153 * 4
        yield self.run_test(content = _gsm0338_str, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 612) # 612 = 153 * 4
        yield self.run_test(content = _ia5ascii_str, datacoding = 1, port = self.httpPort_udh)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass
    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_LATIN_1(self):
        _latin1_str = composeMessage(ISO8859_1, 670) # 670 = 134 * 5
        yield self.run_test(content = _latin1_str, datacoding = 3, port = self.httpPort_udh)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass
    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeMessage({'\x8140', '\x96BA', '\xE062', '\xEAA2'}, 335) # 335 = 67 * 5
        yield self.run_test(content = ''.join(_jisx201_str), datacoding = 5, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        _cyrillic_str = composeMessage(CYRILLIC, 670) # 670 = 134 * 5
        yield self.run_test(content = _cyrillic_str, datacoding = 6, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        _iso8859_8_str = composeMessage(ISO_8859_8, 670) # 670 = 134 * 5
        yield self.run_test(content = _iso8859_8_str, datacoding = 7, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeMessage({'\x0623', '\x0631', '\x0646', '\x0628'}, 335) # 335 = 67 * 5
        yield self.run_test(content = ''.join(_rabbit_arabic), datacoding = 8, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _cp932_str, datacoding = 9, port = self.httpPort_udh)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass
    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({'\x8B89', '\x8B90', '\x8BC9', '\xFC4B'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _cp932_str, datacoding = 13, port = self.httpPort_udh)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeMessage({'\x8141', '\xA496', '\xE1D7', '\xFDFE'}, 335) # 335 = 67 * 5
        yield self.run_test(content = _ks_c_5601_str, datacoding = 14, port = self.httpPort_udh)