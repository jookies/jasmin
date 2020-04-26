import random

from twisted.internet import reactor
from twisted.web.client import Agent
from twisted.internet import defer
from testfixtures import LogCapture
from jasmin.routing.proxies import RouterPBProxy
from treq import text_content
from treq.client import HTTPClient

from tests.routing.test_router import HappySMSCTestCase, SubmitSmTestCaseTools

def composeMessage(characters, length):
    if length <= len(characters):
        return b''.join(random.sample(characters, length))
    else:
        s = b''
        while len(s) < length:
            s += b''.join(random.sample(characters, len(characters)))
        return s[:length]

class LoggingTestCases(RouterPBProxy, HappySMSCTestCase, SubmitSmTestCaseTools):
    @defer.inlineCallbacks
    def run_test(self, content, datacoding=None, port=1401):
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


class SubmitSmTestCases(LoggingTestCases):
    @defer.inlineCallbacks
    def test_submit_sm(self):
        """Reference to #29:
        Double logging of SMS-MT
        """
        lc = LogCapture("jasmin-sm-listener")

        _gsm0338_str = composeMessage([b'A', b'B', b'C'], 160)
        yield self.run_test(content=_gsm0338_str)

        # Assert logging
        loggedMsgCount = 0
        for record in lc.records:
            if record.getMessage()[:6] == 'SMS-MT':
                loggedMsgCount += 1
        self.assertEqual(loggedMsgCount, 1)
