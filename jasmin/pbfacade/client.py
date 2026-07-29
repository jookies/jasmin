import json
import uuid
from urllib import error, request


PROTOCOL_VERSION = "jasmin.pb-facade.v1"


class FacadeError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


class GoFacadeClient:
    """Small synchronous client; avatars dispatch it through Twisted's pool."""

    def __init__(self, base_url, token, timeout=30):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def call(self, method, params=None):
        payload = {
            "version": PROTOCOL_VERSION,
            "id": str(uuid.uuid4()),
            "method": method,
            "params": params or {},
        }
        encoded = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        req = request.Request(
            self.base_url + "/v1/call",
            data=encoded,
            method="POST",
            headers={
                "Authorization": "Bearer " + self.token,
                "Content-Type": "application/json",
            },
        )
        try:
            with request.urlopen(req, timeout=self.timeout) as response:
                result = json.load(response)
        except error.HTTPError as exc:
            try:
                result = json.load(exc)
            except Exception as decode_error:
                raise FacadeError("transport_error", str(exc)) from decode_error
        except (error.URLError, TimeoutError) as exc:
            raise FacadeError("transport_error", str(exc)) from exc
        if result.get("version") != PROTOCOL_VERSION:
            raise FacadeError("protocol_error", "unexpected facade version")
        if not result.get("ok"):
            failure = result.get("error") or {}
            raise FacadeError(failure.get("code", "internal_error"), failure.get("message", "facade call failed"))
        return result.get("result")
