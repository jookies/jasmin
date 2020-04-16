# https://gist.github.com/1873035#file_twisted_web_test_utils.py

import json
from io import StringIO

from twisted.internet.defer import succeed
from twisted.web import server, http_headers
from twisted.web.test.test_web import DummyRequest
from twisted.web.http import Request


class SmartDummyRequest(DummyRequest):
    def __init__(self, method, url, args=None, json_data=None, headers=None):
        DummyRequest.__init__(self, url.split(b'/'))
        self.method = method
        self.requestHeaders = http_headers.Headers(headers or {})

        # set args
        args = args or {}
        for k, v in args.items():
            if isinstance(k, str):
                k = k.encode()
            if isinstance(v, str):
                v = v.encode()
            self.addArg(k, v)

        if json_data is not None:
            self.content = StringIO(json.dumps(json_data))
        else:
            self.content = None

    def value(self):
        return b"".join(self.written)


class DummySite(server.Site):
    def get(self, url, args=None):
        return self._request(b"GET", url, args)

    def post(self, url, args=None, json_data=None, headers=None):
        if json_data is not None:
            return self._request_json(b"POST", url, json_data, headers=headers)
        else:
            return self._request(b"POST", url, args, headers=headers)

    def _request(self, method, url, args, headers=None):
        request = SmartDummyRequest(method, url, args, headers=headers)
        resource = self.getResourceFor(request)
        result = resource.render(request)
        return self._resolveResult(request, result)

    def _request_json(self, method, url, json_data, headers=None):
        request = SmartDummyRequest(method, url, json_data=json_data, headers=headers)
        resource = self.getResourceFor(request)
        result = resource.render(request)
        return self._resolveResult(request, result)

    def _resolveResult(self, request, result):
        if isinstance(result, str):
            request.write(result.encode())
            request.finish()
            return succeed(request)
        elif isinstance(result, bytes):
            request.write(result)
            request.finish()
            return succeed(request)
        elif result is server.NOT_DONE_YET:
            if request.finished:
                return succeed(request)
            else:
                return request.notifyFinish().addCallback(lambda _: request)
        else:
            raise ValueError("Unexpected return value: %r" % (result,))
