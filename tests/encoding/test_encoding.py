import random
import binascii
import urllib.request, urllib.parse, urllib.error

from twisted.internet import defer
from twisted.internet import reactor
from twisted.web import server
from twisted.web.client import Agent
from treq import text_content
from treq.client import HTTPClient
from smpp.pdu.pdu_types import EsmClassGsmFeatures, DataCodingDefault
import messaging.sms.gsm0338

from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.proxies import RouterPBProxy
from tests.encoding.codepages import (IA5_ASCII, ISO_8859)

from tests.routing.test_router import HappySMSCTestCase, SubmitSmTestCaseTools


def composeMessage(characters, length):
    if length <= len(characters):
        return b''.join(random.sample(characters, length))
    else:
        s = b''
        while len(s) < length:
            s += b''.join(random.sample(characters, len(characters)))
        return s[:length]


def composeUnicodeMessage(characters, length):
    if length <= len(characters):
        return ''.join(random.sample(characters, length))
    else:
        s = ''
        while len(s) < length:
            s += ''.join(random.sample(characters, len(characters)))
        return s[:length]

class CodingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def run_test(self, content, encoded_content=None, datacoding=None, port=1401):
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
        baseurl = 'http://127.0.0.1:%s/send' % port

        if encoded_content is None:
            encoded_content = content

        # Send a MT
        # We should receive a msg id
        agent = Agent(reactor)
        client = HTTPClient(agent)
        response = yield client.post(baseurl, data=self.params)
        text = yield text_content(response)
        msgStatus = text[:7]

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
        datacoding_matrix[0] = {'schemeData': DataCodingDefault.SMSC_DEFAULT_ALPHABET}
        datacoding_matrix[1] = {'schemeData': DataCodingDefault.IA5_ASCII}
        datacoding_matrix[2] = {'schemeData': DataCodingDefault.OCTET_UNSPECIFIED}
        datacoding_matrix[3] = {'schemeData': DataCodingDefault.LATIN_1}
        datacoding_matrix[4] = {'schemeData': DataCodingDefault.OCTET_UNSPECIFIED_COMMON}
        datacoding_matrix[5] = {'schemeData': DataCodingDefault.JIS}
        datacoding_matrix[6] = {'schemeData': DataCodingDefault.CYRILLIC}
        datacoding_matrix[7] = {'schemeData': DataCodingDefault.ISO_8859_8}
        datacoding_matrix[8] = {'schemeData': DataCodingDefault.UCS2}
        datacoding_matrix[9] = {'schemeData': DataCodingDefault.PICTOGRAM}
        datacoding_matrix[10] = {'schemeData': DataCodingDefault.ISO_2022_JP}
        datacoding_matrix[13] = {'schemeData': DataCodingDefault.EXTENDED_KANJI_JIS}
        datacoding_matrix[14] = {'schemeData': DataCodingDefault.KS_C_5601}

        # Check for content encoding
        receivedContent = b''
        for submitSm in self.SMSCPort.factory.lastClient.submitRecords:
            if (EsmClassGsmFeatures.UDHI_INDICATOR_SET in submitSm.params['esm_class'].gsmFeatures and
               submitSm.params['short_message'][:3] == b'\x05\x00\x03'):
                receivedContent += submitSm.params['short_message'][6:]
            else:
                receivedContent += submitSm.params['short_message']

        self.assertEqual(encoded_content, receivedContent)

        # Check for schemeData
        sentDataCoding = datacoding_matrix[datacoding]['schemeData']
        for submitSm in self.SMSCPort.factory.lastClient.submitRecords:
            self.assertEqual(submitSm.params['data_coding'].schemeData, sentDataCoding)


class SubmitSmCodingTestCases(CodingTestCases):
    @defer.inlineCallbacks
    def test_gsm0338_at(self):
        """Testing gsm338 encoding for the @ char"""
        _gsm0338_str = ('@' * 160)
        yield self.run_test(content=_gsm0338_str, encoded_content=_gsm0338_str.encode('gsm0338'))

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 160)
        yield self.run_test(content=_ia5ascii_str, datacoding=1)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass

    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_ISO_8859_1(self):
        self.assertEqual(b'\xD0\xFE', '\u00D0\u00FE'.encode('iso-8859-1'))
        _latin1_str = composeMessage(ISO_8859, 140)
        yield self.run_test(content=_latin1_str, datacoding=3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        # .encode('shift_jis')
        self.assertEqual(b'\x81\x40\x96\xBA\xE0\x62\xEA\xA2', '\u3000\u5A18\u7009\u7464'.encode('shift_jis'))
        _jisx201_str = composeUnicodeMessage({'\u3000', '\u5A18', '\u7009', '\u7464'}, 70)
        yield self.run_test(content=_jisx201_str.encode('shift_jis'), datacoding=5)

    @defer.inlineCallbacks
    def test_ISO_8859_5(self):
        # .encode('iso-8859-5')
        self.assertEqual(b'\xA6\xE9', '\u0406\u0449'.encode('iso-8859-5'))
        _cyrillic_str = composeMessage(ISO_8859, 140)
        yield self.run_test(content=_cyrillic_str, datacoding=6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        self.assertEqual(b'\xED\xFD', '\u05DD\u200E'.encode('iso-8859-8'))
        _iso8859_8_str = composeMessage(ISO_8859, 140)
        yield self.run_test(content=_iso8859_8_str, datacoding=7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        # .encode('utf_16_be')
        _rabbit_arabic = composeUnicodeMessage({'\u0623', '\u0631', '\u0646', '\u0628'}, 70)
        self.assertEqual(b'\x06\x23'.decode('utf_16_be'), '\u0623')
        yield self.run_test(content=_rabbit_arabic.encode('utf_16_be'), datacoding=8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # https://en.wikipedia.org/wiki/Short_Message_Peer-to-Peer#Unclear_support_for_Shift-JIS_encoding
        _pictogram_str = composeUnicodeMessage({'\u7D1A', '\u5DE8', '\u6975', '\u6DD8'}, 70)
        yield self.run_test(content=_pictogram_str.encode('shift_jis'), datacoding=9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0212.TXT
        # https://github.com/python/cpython/blob/master/Modules/cjkcodecs/_codecs_iso2022.c#L1090
        # Need to set ENV var UNICODEMAP_JP to unicode-ascii
        # https://doc.qt.io/qtforpython/overviews/codecs-jis.html
        self.assertEqual(b'\x31\x24\x4C\x4B', '\u4F8C\u7480'.encode('iso2022jp-1').lstrip(b'\x1b$(D').rstrip(b'\x1b(B'))
        self.assertEqual('\u4F8C\u7480', (b'\x1b$(D' + b'\x31\x24\x4C\x4B' + b'\x1b(B').decode('iso2022jp-1'))
        jisx0212_str = composeUnicodeMessage({'\u4F8C', '\u7480', '\u5B94', '\u9835'}, 70)
        yield self.run_test(content=jisx0212_str.encode('iso2022jp-1').lstrip(b'\x1b$(D').rstrip(b'\x1b(B'), datacoding=13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        # .encode('ksc5601')
        self.assertEqual(b'\xA5\x55\xA4\xBA', '\uCA64\u314A'.encode('ksc5601'))
        _ks_c_5601_str = composeUnicodeMessage({'\uAC02', '\uCA3C', '\u6D91', '\u8A70'}, 70)
        yield self.run_test(content=_ks_c_5601_str.encode('ksc5601'), datacoding=14)

    test_KS_C_5601.skip = 'ksc5601 encoding in python does not match the mappings expected'

class LongSubmitSmCodingUsingSARTestCases(CodingTestCases):
    @defer.inlineCallbacks
    def test_gsm0338_at(self):
        _gsm0338_str = ('@' * 612)  # 612 = 153 * 4
        yield self.run_test(content=_gsm0338_str, encoded_content=_gsm0338_str.encode('gsm0338'))

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 612)  # 612 = 153 * 4
        yield self.run_test(content=_ia5ascii_str, datacoding=1)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass

    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_ISO_8859_1(self):
        # .encode('iso-8859-1')
        _latin1_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_latin1_str, datacoding=3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeUnicodeMessage({'\u3000', '\u5A18', '\u7009', '\u7464'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_jisx201_str.encode('shift_jis'), datacoding=5)

    @defer.inlineCallbacks
    def test_ISO_8859_5(self):
        # .encode('iso-8859-5')
        _cyrillic_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_cyrillic_str, datacoding=6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        _iso8859_8_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_iso8859_8_str, datacoding=7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeUnicodeMessage({'\u0623', '\u0631', '\u0646', '\u0628'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_rabbit_arabic.encode('utf_16_be'), datacoding=8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # https://en.wikipedia.org/wiki/Short_Message_Peer-to-Peer#Unclear_support_for_Shift-JIS_encoding
        _pictogram_str = composeUnicodeMessage({'\u7D1A', '\u5DE8', '\u6975', '\u6DD8'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_pictogram_str.encode('shift_jis'), datacoding=9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0212.TXT
        jisx0212_str = composeUnicodeMessage({'\u4F8C', '\u7480', '\u5B94', '\u9835'}, 335)
        yield self.run_test(content=jisx0212_str.encode('iso2022jp-1').lstrip(b'\x1b$(D').rstrip(b'\x1b(B'), datacoding=13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeUnicodeMessage({'\uAC02', '\uCA3C', '\u6D91', '\u8A70'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_ks_c_5601_str.encode('ksc5601'), datacoding=14)

    test_KS_C_5601.skip = 'ksc5601 encoding in python does not match the mappings expected'

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
        self.httpPort_udh = httpApiConfigInstance.port

    @defer.inlineCallbacks
    def tearDown(self):
        yield CodingTestCases.tearDown(self)
        self.httpServer_udh.stopListening()

    @defer.inlineCallbacks
    def test_gsm0338_at(self):
        _gsm0338_str = ('@' * 612)
        yield self.run_test(content=_gsm0338_str, encoded_content=_gsm0338_str.encode('gsm0338'), port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_IA5_ASCII(self):
        _ia5ascii_str = composeMessage(IA5_ASCII, 612)  # 612 = 153 * 4
        yield self.run_test(content=_ia5ascii_str, datacoding=1, port=self.httpPort_udh)

    def test_OCTET_UNSPECIFIED(self):
        # datacoding = 2
        pass

    test_OCTET_UNSPECIFIED.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_ISO_8859_1(self):
        # .encode('iso-8859-1')
        _latin1_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_latin1_str, datacoding=3, port=self.httpPort_udh)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeUnicodeMessage({'\u3000', '\u5A18', '\u7009', '\u7464'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_jisx201_str.encode('shift_jis'), datacoding=5, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_ISO_8859_5(self):
        # .encode('iso-8859-5')
        _cyrillic_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_cyrillic_str, datacoding=6, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        _iso8859_8_str = composeMessage(ISO_8859, 670)  # 670 = 134 * 5
        yield self.run_test(content=_iso8859_8_str, datacoding=7, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeUnicodeMessage({'\u0623', '\u0631', '\u0646', '\u0628'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_rabbit_arabic.encode('utf_16_be'), datacoding=8, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # https://en.wikipedia.org/wiki/Short_Message_Peer-to-Peer#Unclear_support_for_Shift-JIS_encoding
        _pictogram_str = composeUnicodeMessage({'\u7D1A', '\u5DE8', '\u6975', '\u6DD8'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_pictogram_str.encode('shift_jis'), datacoding=9, port=self.httpPort_udh)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. https://www.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/JIS0212.TXT
        jisx0212_str = composeUnicodeMessage({'\u4F8C', '\u7480', '\u5B94', '\u9835'}, 70)
        yield self.run_test(content=jisx0212_str.encode('iso2022jp-1').lstrip(b'\x1b$(D').rstrip(b'\x1b(B'), datacoding=13, port=self.httpPort_udh)
        pass

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeUnicodeMessage({'\uAC02', '\uCA3C', '\u6D91', '\u8A70'}, 335)  # 335 = 67 * 5
        # self.assertTrue(False)
        yield self.run_test(content=_ks_c_5601_str.encode('ksc5601'), datacoding=14, port=self.httpPort_udh)

    test_KS_C_5601.skip = 'ksc5601 encoding in python does not match the mappings expected'
