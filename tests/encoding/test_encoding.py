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
from tests.routing.codepages import (IA5_ASCII, ISO8859_1,
                                           CYRILLIC, ISO_8859_8)

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
    def test_LATIN_1(self):
        _latin1_str = composeMessage(ISO8859_1, 140)
        yield self.run_test(content=_latin1_str, datacoding=3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        # .encode('shift_jis')
        _jisx201_str = composeMessage({b'\x81\x40', b'\x96\xBA', b'\xE0\x62', b'\xEA\xA2'}, 70)
        yield self.run_test(content=_jisx201_str, datacoding=5)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        # .encode('iso-8859-5')
        _cyrillic_str = composeMessage(CYRILLIC, 140)
        yield self.run_test(content=_cyrillic_str, datacoding=6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        _iso8859_8_str = composeMessage(ISO_8859_8, 140)
        yield self.run_test(content=_iso8859_8_str, datacoding=7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        # .encode('utf_16_be')
        _rabbit_arabic = composeMessage({b'\x06\x23', b'\x06\x31', b'\x06\x46', b'\x06\x28'}, 70)
        yield self.run_test(content=_rabbit_arabic, datacoding=8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        # .encode('cp932')
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 70)
        yield self.run_test(content=_cp932_str, datacoding=9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        # .encode('cp932')
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 70)
        yield self.run_test(content=_cp932_str, datacoding=13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        # .encode('ksc5601')
        _ks_c_5601_str = composeMessage({b'\x81\x41', b'\xA4\x96', b'\xE1\xD7', b'\xFD\xFE'}, 70)
        yield self.run_test(content=_ks_c_5601_str, datacoding=14)


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
    def test_LATIN_1(self):
        # .encode('iso-8859-1')
        _latin1_str = composeMessage(ISO8859_1, 670)  # 670 = 134 * 5
        yield self.run_test(content=_latin1_str, datacoding=3)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeMessage({b'\x81\x40', b'\x96\xBA', b'\xE0\x62', b'\xEA\xA2'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_jisx201_str, datacoding=5)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        # .encode('iso-8859-5')
        _cyrillic_str = composeMessage(CYRILLIC, 670)  # 670 = 134 * 5
        yield self.run_test(content=_cyrillic_str, datacoding=6)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        _iso8859_8_str = composeMessage(ISO_8859_8, 670)  # 670 = 134 * 5
        yield self.run_test(content=_iso8859_8_str, datacoding=7)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeMessage({b'\x06\x23', b'\x06\x31', b'\x06\x46', b'\x06\x28'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_rabbit_arabic, datacoding=8)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_cp932_str, datacoding=9)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_cp932_str, datacoding=13)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeMessage({b'\x81\x41', b'\xA4\x96', b'\xE1\xD7', b'\xFD\xFE'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_ks_c_5601_str, datacoding=14)


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
    def test_LATIN_1(self):
        # .encode('iso-8859-1')
        _latin1_str = composeMessage(ISO8859_1, 670)  # 670 = 134 * 5
        yield self.run_test(content=_latin1_str, datacoding=3, port=self.httpPort_udh)

    def test_OCTET_UNSPECIFIED_COMMON(self):
        # datacoding = 4
        pass

    test_OCTET_UNSPECIFIED_COMMON.skip = 'TODO: What kind of data should we send using this DC ?'

    @defer.inlineCallbacks
    def test_JIS(self):
        # c.f. http://unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/JIS/SHIFTJIS.TXT
        _jisx201_str = composeMessage({b'\x81\x40', b'\x96\xBA', b'\xE0\x62', b'\xEA\xA2'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_jisx201_str, datacoding=5, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_CYRILLIC(self):
        # .encode('iso-8859-5')
        _cyrillic_str = composeMessage(CYRILLIC, 670)  # 670 = 134 * 5
        yield self.run_test(content=_cyrillic_str, datacoding=6, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_ISO_8859_8(self):
        # .encode('iso-8859-8')
        _iso8859_8_str = composeMessage(ISO_8859_8, 670)  # 670 = 134 * 5
        yield self.run_test(content=_iso8859_8_str, datacoding=7, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_UCS2(self):
        _rabbit_arabic = composeMessage({b'\x06\x23', b'\x06\x31', b'\x06\x46', b'\x06\x28'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_rabbit_arabic, datacoding=8, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_PICTOGRAM(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_cp932_str, datacoding=9, port=self.httpPort_udh)

    def test_ISO_2022_JP(self):
        # datacoding = 10
        pass

    test_ISO_2022_JP.skip = 'TODO: Didnt find unicode codepage for ISO2022-JP'

    @defer.inlineCallbacks
    def test_EXTENDED_KANJI_JIS(self):
        # c.f. http://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/WINDOWS/CP932.TXT
        _cp932_str = composeMessage({b'\x8B\x89', b'\x8B\x90', b'\x8B\xC9', b'\xFC\x4B'}, 335)  # 335 = 67 * 5
        yield self.run_test(content=_cp932_str, datacoding=13, port=self.httpPort_udh)

    @defer.inlineCallbacks
    def test_KS_C_5601(self):
        # c.f. ftp://ftp.unicode.org/Public/MAPPINGS/OBSOLETE/EASTASIA/KSC/KSC5601.TXT
        _ks_c_5601_str = composeMessage({b'\x81\x41', b'\xA4\x96', b'\xE1\xD7', b'\xFD\xFE'}, 335)  # 335 = 67 * 5
        # self.assertTrue(False)
        yield self.run_test(content=_ks_c_5601_str, datacoding=14, port=self.httpPort_udh)
