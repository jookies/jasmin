from twisted.internet import defer

from .test_server import HTTPApiTestCases


class MetricsTestCases(HTTPApiTestCases):
    @defer.inlineCallbacks
    def get_metric(self, key=None):
        """
        Invoke /metrics,
         if key is set: return key's value or None if not found
         if not: return metrics in json format
        """
        response = yield self.web.get(b'metrics')

        metric_map = {}
        for _line in response.value().decode().split('\n'):
            if len(_line) == 0 or _line[:1] == '#':
                continue
            _key, _value = _line.split(' ')
            metric_map[_key] = _value

        if key is not None:
            yield defer.returnValue(metric_map.get(_key, None))
        else:
            yield defer.returnValue(metric_map)


class HttpapiTestCases(MetricsTestCases):
    username = 'nathalie'

    @defer.inlineCallbacks
    def test_send_auth_error(self):
        _before = yield self.get_metric()

        response = yield self.web.post(b'send', {b'username': self.username,
                                      b'password': 'incorrec',
                                      b'to': b'06155423',
                                      b'content': 'anycontent'})
        self.assertEqual(response.value(),
                         b'Error "Authentication failure for username:%s"' % self.username.encode())
        self.assertEqual(response.responseCode, 403)

        _after = yield self.get_metric()
        self.assertEqual(int(_before['httpapi_request_count'].encode()) + 1,
                         int(_after['httpapi_request_count'].encode()))
        self.assertEqual(int(_before['httpapi_auth_error_count'].encode()) + 1,
                         int(_after['httpapi_auth_error_count'].encode()))

    @defer.inlineCallbacks
    def test_send_other_error(self):
        _before = yield self.get_metric()

        response = yield self.web.post(b'send', {b'username': 'nathalie',
                                                 b'password': 'correct',
                                                 b'to': b'06155423',
                                                 b'content': 'anycontent'})
        self.assertEqual(response.value(),
                         b'Error "Cannot send submit_sm, check SMPPClientManagerPB log file for details"')
        self.assertEqual(response.responseCode, 500)

        _after = yield self.get_metric()
        self.assertEqual(int(_before['httpapi_request_count'].encode()) + 1,
                         int(_after['httpapi_request_count'].encode()))
        self.assertEqual(int(_before['httpapi_server_error_count'].encode()) + 1,
                         int(_after['httpapi_server_error_count'].encode()))
