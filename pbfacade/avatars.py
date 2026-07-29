"""Twisted PB avatars translating frozen Python objects into normalized JSON.

This module is the only migration component permitted to call pickle.loads on
network-supplied values. It must run on a trusted/internal listener.
"""

import base64
import datetime
import hashlib
import json
import logging
import math
import pickle
import re
import struct

from twisted.internet import threads
from twisted.spread import pb

import jasmin
from pbfacade.client import FacadeError
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.Filters import (
    ConnectorFilter,
    DateIntervalFilter,
    DestinationAddrFilter,
    EvalPyFilter,
    GroupFilter,
    ShortMessageFilter,
    SourceAddrFilter,
    TagFilter,
    TimeIntervalFilter,
    TransparentFilter,
    UserFilter,
)
from jasmin.routing.Interceptors import (
    DefaultInterceptor,
    StaticMOInterceptor,
    StaticMTInterceptor,
)
from jasmin.routing.Routes import (
    DefaultRoute,
    RandomRoundrobinMORoute,
    RandomRoundrobinMTRoute,
    StaticMORoute,
    StaticMTRoute,
)
from jasmin.routing.jasminApi import (
    Group,
    HttpConnector,
    MOInterceptorScript,
    MTInterceptorScript,
    SmppClientConnector,
    SmppServerSystemIdConnector,
    User,
)
from jasmin.tools.eval import CompiledNode
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.pdu_types import AddrNpi, AddrTon, PriorityFlag, ReplaceIfPresentFlag


def _bytes(value):
    if value is None:
        return ""
    if isinstance(value, str):
        value = value.encode()
    return base64.b64encode(value).decode("ascii")


def _enum(value, default=0):
    if value is None:
        return default
    if hasattr(value, "_value_"):
        return int(value._value_)
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


_ADDR_TON_WIRE = {
    "UNKNOWN": 0,
    "INTERNATIONAL": 1,
    "NATIONAL": 2,
    "NETWORK_SPECIFIC": 3,
    "SUBSCRIBER_NUMBER": 4,
    "ALPHANUMERIC": 5,
    "ABBREVIATED": 6,
}
_ADDR_NPI_WIRE = {
    "UNKNOWN": 0,
    "ISDN": 1,
    "DATA": 3,
    "TELEX": 4,
    "LAND_MOBILE": 6,
    "NATIONAL": 8,
    "PRIVATE": 9,
    "ERMES": 10,
    "INTERNET": 14,
    "WAP_CLIENT_ID": 18,
}
_PRIORITY_WIRE = {"LEVEL_0": 0, "LEVEL_1": 1, "LEVEL_2": 2, "LEVEL_3": 3}
_REPLACE_WIRE = {"DO_NOT_REPLACE": 0, "REPLACE": 1}


def _wire_enum(value, mapping, default=0):
    if value is None:
        return default
    name = getattr(value, "name", None)
    if name in mapping:
        return mapping[name]
    return _enum(value, default)


def _legacy_enum(enum_type, value, mapping, default_name):
    try:
        wire = int(value)
    except (TypeError, ValueError):
        wire = mapping[default_name]
    name = next((name for name, encoded in mapping.items() if encoded == wire), default_name)
    return getattr(enum_type, name)


def _pattern(value):
    if value is None:
        return ""
    value = getattr(value, "pattern", value)
    if isinstance(value, bytes):
        return value.decode("latin1")
    return str(value)


def _legacy_pickle(value):
    return base64.b64encode(pickle.dumps(value, pickle.HIGHEST_PROTOCOL)).decode("ascii")


def _unpickle_spec(spec):
    encoded = spec.get("legacy_pickle")
    if not encoded:
        return None
    return pickle.loads(base64.b64decode(encoded))


def group_object(spec, fallback_id=None):
    value = _unpickle_spec(spec)
    if value is None:
        value = Group(str(spec.get("gid") or fallback_id))
    value.enabled = not spec.get("disabled", False)
    return value


def user_object(spec, fallback_id=None):
    value = _unpickle_spec(spec)
    if value is None:
        group = Group(str(spec.get("group_id") or "default"))
        digest = spec.get("password_md5") or spec.get("password_sha256") or ""
        try:
            password = bytes.fromhex(digest)
        except (TypeError, ValueError):
            password = b""
        value = User(
            str(spec.get("external_id") or fallback_id or spec.get("username")),
            group,
            spec.get("username") or fallback_id,
            password,
            password_crypted=True,
        )
    return apply_user_spec(value, spec)


def filter_object(spec):
    kind = spec.get("type", "transparent")
    if kind == "transparent":
        return TransparentFilter()
    if kind == "connector":
        return ConnectorFilter(SmppClientConnector(str(spec.get("value") or spec.get("connector_id"))))
    if kind == "user":
        username = str(spec.get("username"))
        return UserFilter(User(username, Group("default"), username, b"", password_crypted=True))
    if kind == "group":
        return GroupFilter(Group(str(spec.get("group_id"))))
    if kind == "source_addr":
        return SourceAddrFilter(spec.get("pattern", ""))
    if kind == "destination_addr":
        return DestinationAddrFilter(spec.get("pattern", ""))
    if kind == "short_message":
        return ShortMessageFilter(spec.get("pattern", ""))
    if kind == "date_interval":
        return DateIntervalFilter([
            datetime.date.fromisoformat(spec["start"]),
            datetime.date.fromisoformat(spec["end"]),
        ])
    if kind == "time_interval":
        return TimeIntervalFilter([
            datetime.time.fromisoformat(spec["start"]),
            datetime.time.fromisoformat(spec["end"]),
        ])
    if kind == "tag":
        return TagFilter(spec.get("value", ""))
    if kind == "eval_py":
        return EvalPyFilter(spec.get("value", ""))
    raise ValueError("unsupported normalized filter: " + str(kind))


def route_object(spec, direction):
    value = _unpickle_spec(spec)
    if value is not None:
        return value
    filters = [filter_object(item) for item in spec.get("filters", [])]
    if direction == "mt":
        connector_ids = spec.get("connector_ids") or [spec.get("connector_id")]
        connectors = [SmppClientConnector(str(cid)) for cid in connector_ids if cid]
        rate = float(spec.get("rate", 0))
        if spec.get("default", False):
            return DefaultRoute(connectors[0], rate)
        if len(connectors) > 1:
            return RandomRoundrobinMTRoute(filters, connectors, rate)
        return StaticMTRoute(filters, connectors[0], rate)
    target = spec.get("connector") or {}
    if target.get("type") == "http":
        connector = HttpConnector(str(target.get("cid")), target.get("url"), target.get("method", "GET"))
    else:
        connector = SmppServerSystemIdConnector(str(target.get("system_id") or target.get("cid")))
    source_cid = spec.get("filter_connector_id")
    if source_cid:
        filters.insert(0, ConnectorFilter(SmppClientConnector(str(source_cid))))
    if spec.get("default", False):
        return DefaultRoute(connector)
    return StaticMORoute(filters, connector)


def interceptor_object(spec, direction):
    value = _unpickle_spec(spec)
    if value is not None:
        return value
    script_type = MTInterceptorScript if direction == "mt" else MOInterceptorScript
    script = script_type(spec.get("py_code", ""))
    if spec.get("default", False):
        return DefaultInterceptor(script)
    filters = [filter_object(item) for item in spec.get("filters", [])]
    interceptor_type = StaticMTInterceptor if direction == "mt" else StaticMOInterceptor
    return interceptor_type(filters, script)


def group_spec(group, raw_pickle=None):
    return {
        "gid": str(group.gid),
        "disabled": not bool(group.enabled),
        "legacy_pickle": raw_pickle or _legacy_pickle(group),
    }


def mt_credential_spec(credential):
    authorizations = credential.authorizations
    filters = credential.value_filters
    quotas = credential.quotas
    source = credential.defaults.get("source_address")
    return {
        "http_send": authorizations.get("http_send", True),
        "http_bulk": authorizations.get("http_bulk", False),
        "http_balance": authorizations.get("http_balance", True),
        "http_rate": authorizations.get("http_rate", True),
        "smpps_send": authorizations.get("smpps_send", True),
        "http_long_content": authorizations.get("http_long_content", True),
        "set_dlr_level": authorizations.get("set_dlr_level", True),
        "http_set_dlr_method": authorizations.get("http_set_dlr_method", True),
        "set_source_address": authorizations.get("set_source_address", True),
        "set_priority": authorizations.get("set_priority", True),
        "set_validity_period": authorizations.get("set_validity_period", True),
        "set_hex_content": authorizations.get("set_hex_content", True),
        "set_schedule_delivery_time": authorizations.get("set_schedule_delivery_time", True),
        "filter_destination_address": _pattern(filters.get("destination_address")),
        "filter_source_address": _pattern(filters.get("source_address")),
        "filter_priority": _pattern(filters.get("priority")),
        "filter_validity_period": _pattern(filters.get("validity_period")),
        "filter_content": _pattern(filters.get("content")),
        "default_source_address": None if source is None else source.decode("latin1"),
        "http_throughput": quotas.get("http_throughput"),
        "smpps_throughput": quotas.get("smpps_throughput"),
    }


def user_spec(user, raw_pickle=None):
    quotas = user.mt_credential.quotas
    smpps_auth = user.smpps_credential.authorizations
    return {
        "username": user.username,
        "external_id": str(user.uid),
        "password_md5": bytes(user.password).hex(),
        "balance": quotas.get("balance"),
        "submit_sm_count": quotas.get("submit_sm_count"),
        "early_decrement_balance_percent": quotas.get("early_decrement_balance_percent"),
        "group_id": str(user.group.gid),
        "disabled": not bool(user.enabled),
        "mt_credential": mt_credential_spec(user.mt_credential),
        "smpps_credential": {
            "bind": smpps_auth.get("bind", True),
            "ip": smpps_auth.get("ip", "0.0.0.0/0"),
            "max_bindings": user.smpps_credential.quotas.get("max_bindings"),
        },
        "legacy_pickle": raw_pickle or _legacy_pickle(user),
    }


def apply_user_spec(user, spec):
    user.enabled = not spec.get("disabled", False)
    user.group = Group(str(spec.get("group_id") or user.group.gid))
    credential = spec.get("mt_credential") or {}
    for name in ("balance", "submit_sm_count", "early_decrement_balance_percent"):
        user.mt_credential.quotas[name] = spec.get(name)
    for name in ("http_throughput", "smpps_throughput"):
        user.mt_credential.quotas[name] = credential.get(name)
    for name, value in credential.items():
        if name in user.mt_credential.authorizations and isinstance(value, bool):
            user.mt_credential.authorizations[name] = value
    for field, name in (
        ("filter_destination_address", "destination_address"),
        ("filter_source_address", "source_address"),
        ("filter_priority", "priority"),
        ("filter_validity_period", "validity_period"),
        ("filter_content", "content"),
    ):
        if credential.get(field) is not None:
            user.mt_credential.value_filters[name] = re.compile(credential[field].encode("latin1"))
    source = credential.get("default_source_address")
    user.mt_credential.defaults["source_address"] = None if source is None else source.encode("latin1")
    smpps = spec.get("smpps_credential") or {}
    if "bind" in smpps:
        user.smpps_credential.authorizations["bind"] = smpps["bind"]
    if "ip" in smpps:
        user.smpps_credential.authorizations["ip"] = smpps["ip"]
    user.smpps_credential.quotas["max_bindings"] = smpps.get("max_bindings")
    return user


def filter_spec(value):
    name = value.__class__.__name__
    if name == "TransparentFilter":
        return None
    if name == "ConnectorFilter":
        return {"type": "connector", "value": str(value.connector.cid)}
    if name == "UserFilter":
        return {"type": "user", "username": value.user.username}
    if name == "GroupFilter":
        return {"type": "group", "group_id": str(value.group.gid)}
    if name == "SourceAddrFilter":
        return {"type": "source_addr", "pattern": _pattern(value.source_addr)}
    if name == "DestinationAddrFilter":
        return {"type": "destination_addr", "pattern": _pattern(value.destination_addr)}
    if name == "ShortMessageFilter":
        return {"type": "short_message", "pattern": _pattern(value.short_message)}
    if name == "DateIntervalFilter":
        return {"type": "date_interval", "start": value.dateInterval[0].isoformat(), "end": value.dateInterval[1].isoformat()}
    if name == "TimeIntervalFilter":
        return {"type": "time_interval", "start": value.timeInterval[0].isoformat(), "end": value.timeInterval[1].isoformat()}
    if name == "TagFilter":
        return {"type": "tag", "value": str(value.tag)}
    if name == "EvalPyFilter":
        return {"type": "eval_py", "value": value.pyCode}
    raise ValueError("unsupported legacy filter: " + name)


def route_spec(route, order, direction, raw_pickle):
    filters = [item for item in (filter_spec(value) for value in getattr(route, "filters", [])) if item]
    default = route.__class__.__name__ == "DefaultRoute"
    connectors = getattr(route, "connector", None)
    if not isinstance(connectors, list):
        connectors = [connectors]
    if direction == "mt":
        spec = {
            "order": order,
            "default": default,
            "rate": float(getattr(route, "rate", 0)),
            "connector_id": str(connectors[0].cid),
            "legacy_pickle": raw_pickle,
        }
        if len(connectors) > 1:
            spec["connector_ids"] = [str(value.cid) for value in connectors]
        spec["filters"] = [value for value in filters if value.get("type") != "connector"]
        return spec
    connector = connectors[0]
    connector_type = getattr(connector, "_type", "")
    if connector_type == "http":
        target = {"type": "http", "cid": str(connector.cid), "url": connector.baseurl, "method": connector.method}
    else:
        target = {"type": "smpps", "system_id": getattr(connector, "system_id", connector.cid)}
    source = next((value for value in filters if value.get("type") == "connector"), None)
    return {
        "order": order,
        "default": default,
        "filter_connector_id": "" if source is None else source["value"],
        "filters": [value for value in filters if value.get("type") != "connector"],
        "connector": target,
        "legacy_pickle": raw_pickle,
    }


def interceptor_spec(value, order, raw_pickle):
    return {
        "order": order,
        "filters": [item for item in (filter_spec(f) for f in getattr(value, "filters", [])) if item],
        "py_code": value.getScript().pyCode,
        "legacy_pickle": raw_pickle,
    }


def connector_spec(config):
    return {
        "cid": config.id,
        "host": config.host,
        "port": config.port,
        "system_id": config.username,
        "password": config.password,
        "system_type": config.systemType,
        "bind": config.bindOperation,
        "addr_ton": _wire_enum(config.addressTon, _ADDR_TON_WIRE),
        "addr_npi": _wire_enum(config.addressNpi, _ADDR_NPI_WIRE),
        "address_range": config.addressRange or "",
        "src_ton": _wire_enum(config.source_addr_ton, _ADDR_TON_WIRE),
        "src_npi": _wire_enum(config.source_addr_npi, _ADDR_NPI_WIRE),
        "dst_ton": _wire_enum(config.dest_addr_ton, _ADDR_TON_WIRE),
        "dst_npi": _wire_enum(config.dest_addr_npi, _ADDR_NPI_WIRE),
        "service_type": config.service_type or "",
        "protocol_id": _enum(config.protocol_id),
        "replace_if_present_flag": _wire_enum(config.replace_if_present_flag, _REPLACE_WIRE),
        "sm_default_msg_id": config.sm_default_msg_id,
        "source_addr": config.source_addr or "",
        "trx_to": config.inactivityTimerSecs,
        "res_to": config.responseTimerSecs,
        "pdu_to": config.pduReadTimerSecs,
        "con_loss_retry": config.reconnectOnConnectionLoss,
        "con_fail_retry": config.reconnectOnConnectionFailure,
        "con_loss_delay": config.reconnectOnConnectionLossDelay,
        "con_fail_delay": config.reconnectOnConnectionFailureDelay,
        "tls_enabled": config.useSSL,
        "elink_interval": config.enquireLinkTimerSecs,
        "bind_to": config.sessionInitTimerSecs,
        "requeue_delay": config.requeue_delay,
        "data_coding": config.data_coding,
        "validity_period": "" if config.validity_period is None else str(config.validity_period),
        "log_file": config.log_file,
        "log_rotate": config.log_rotate,
        "log_privacy": config.log_privacy,
        "priority": _wire_enum(config.priority_flag, _PRIORITY_WIRE),
        "log_level": logging.getLevelName(config.log_level),
        "submit_sm_throughput": config.submit_sm_throughput,
        "dlr_msg_id_bases": config.dlr_msg_id_bases,
        "dlr_expiry": int(config.dlr_expiry),
        "custom_tlvs": config.custom_tlvs,
    }


def connector_object(spec):
    level = spec.get("log_level", logging.INFO)
    if isinstance(level, str):
        level = logging._nameToLevel.get(level.upper(), logging.INFO)
    reverse = {
        "id": spec["cid"], "host": spec["host"], "port": spec["port"],
        "username": spec["system_id"], "password": spec["password"],
        "systemType": spec.get("system_type", ""), "bindOperation": spec.get("bind", "transceiver"),
        "addressTon": _legacy_enum(AddrTon, spec.get("addr_ton"), _ADDR_TON_WIRE, "UNKNOWN"),
        "addressNpi": _legacy_enum(AddrNpi, spec.get("addr_npi"), _ADDR_NPI_WIRE, "UNKNOWN"),
        "addressRange": spec.get("address_range") or None,
        "source_addr_ton": _legacy_enum(AddrTon, spec.get("src_ton"), _ADDR_TON_WIRE, "NATIONAL"),
        "source_addr_npi": _legacy_enum(AddrNpi, spec.get("src_npi"), _ADDR_NPI_WIRE, "ISDN"),
        "dest_addr_ton": _legacy_enum(AddrTon, spec.get("dst_ton"), _ADDR_TON_WIRE, "INTERNATIONAL"),
        "dest_addr_npi": _legacy_enum(AddrNpi, spec.get("dst_npi"), _ADDR_NPI_WIRE, "ISDN"),
        "service_type": spec.get("service_type") or None, "protocol_id": spec.get("protocol_id"),
        "replace_if_present_flag": _legacy_enum(
            ReplaceIfPresentFlag, spec.get("replace_if_present_flag"), _REPLACE_WIRE, "DO_NOT_REPLACE"
        ),
        "sm_default_msg_id": spec.get("sm_default_msg_id", 0), "source_addr": spec.get("source_addr") or None,
        "inactivityTimerSecs": spec.get("trx_to", 300), "responseTimerSecs": spec.get("res_to", 120),
        "pduReadTimerSecs": spec.get("pdu_to", 10), "reconnectOnConnectionLoss": spec.get("con_loss_retry", True),
        "reconnectOnConnectionFailure": spec.get("con_fail_retry", True),
        "reconnectOnConnectionLossDelay": spec.get("con_loss_delay", 10),
        "reconnectOnConnectionFailureDelay": spec.get("con_fail_delay", 10),
        "useSSL": spec.get("tls_enabled", False), "sessionInitTimerSecs": spec.get("bind_to", 30),
        "enquireLinkTimerSecs": spec.get("elink_interval", 30), "requeue_delay": spec.get("requeue_delay", 120),
        "data_coding": spec.get("data_coding", 0), "validity_period": spec.get("validity_period") or None,
        "priority_flag": _legacy_enum(PriorityFlag, spec.get("priority"), _PRIORITY_WIRE, "LEVEL_0"),
        "submit_sm_throughput": spec.get("submit_sm_throughput", 1),
        "dlr_msg_id_bases": spec.get("dlr_msg_id_bases", 0), "dlr_expiry": spec.get("dlr_expiry", 86400),
        "custom_tlvs": spec.get("custom_tlvs", []), "log_file": spec.get("log_file"),
        "log_rotate": spec.get("log_rotate", "midnight"), "log_privacy": spec.get("log_privacy", False),
        "log_level": level,
    }
    return SMPPClientConfig(**{key: value for key, value in reverse.items() if value is not None})


def pdu_body(pdu):
    params = pdu.params
    return {
        "ServiceType": _bytes(params.get("service_type")),
        "SourceAddressTON": _enum(params.get("source_addr_ton")),
        "SourceAddressNPI": _enum(params.get("source_addr_npi")),
        "SourceAddress": _bytes(params.get("source_addr")),
        "DestinationAddressTON": _enum(params.get("dest_addr_ton")),
        "DestinationAddressNPI": _enum(params.get("dest_addr_npi")),
        "DestinationAddress": _bytes(params.get("destination_addr")),
        "ESMClass": _enum(params.get("esm_class")),
        "ProtocolID": _enum(params.get("protocol_id")),
        "PriorityFlag": _enum(params.get("priority_flag")),
        "ScheduleDeliveryTime": _bytes(b""),
        "ValidityPeriod": _bytes(b""),
        "RegisteredDelivery": _enum(params.get("registered_delivery")),
        "ReplaceIfPresentFlag": _enum(params.get("replace_if_present_flag")),
        "DataCoding": _enum(params.get("data_coding")),
        "SMDefaultMessageID": _enum(params.get("sm_default_msg_id")),
        "ShortMessage": _bytes(params.get("short_message") or params.get("message_payload") or b""),
        "Optional": {},
    }


def pdu_wire(pdu):
    return base64.b64encode(PDUEncoder().encode(pdu)).decode("ascii")


def pdu_wires(pdu, max_parts=255):
    """Encode an ordered SubmitSM.nextPdu chain without following cycles."""
    result = []
    seen = set()
    current = pdu
    while current is not None:
        identity = id(current)
        if identity in seen:
            raise ValueError("cyclic SubmitSM.nextPdu chain")
        if len(result) >= max_parts:
            raise ValueError("SubmitSM.nextPdu chain exceeds %d parts" % max_parts)
        seen.add(identity)
        result.append(pdu_wire(current))
        current = getattr(current, "nextPdu", None)
    return result


def bill_spec(value):
    if value is None:
        return None
    if isinstance(value, (bytes, bytearray)):
        value = pickle.loads(value)
    return {
        "id": str(value.bid),
        "submit_sm_amount": float(value.getAmount("submit_sm")),
        "submit_sm_resp_amount": float(value.getAmount("submit_sm_resp")),
        "decrement_submit_sm_count": int(value.getAction("decrement_submit_sm_count")),
    }


class BaseAvatar(pb.Avatar):
    def __init__(self, client):
        self.client = client
        self.avatar = None
        self.log = logging.getLogger("jasmin-pb-facade")

    def setAvatar(self, avatar):
        self.avatar = avatar

    def _defer(self, function, *args):
        return threads.deferToThread(function, *args)

    def _defer_bool(self, function, *args):
        result = self._defer(function, *args)
        result.addCallback(lambda value: value is not False)
        return result

    def _safe(self, method, params=None):
        try:
            return self.client.call(method, params)
        except FacadeError as exc:
            self.log.error("PB facade %s failed [%s]: %s", method, exc.code, exc)
            return False

    def perspective_version_release(self):
        return jasmin.get_release()

    def perspective_version(self):
        return jasmin.get_version()


class RouterAvatar(BaseAvatar):
    def perspective_persist(self, profile="jcli-prod", scope="all"):
        return self._defer(self._safe, "profile.persist", {"profile": profile, "scope": scope})

    def perspective_load(self, profile="jcli-prod", scope="all"):
        return self._defer(self._safe, "profile.load", {"profile": profile, "scope": scope})

    def perspective_is_persisted(self):
        return self._defer(self._safe, "profile.is_persisted", {})

    def perspective_group_add(self, value):
        group = pickle.loads(value)
        return self._defer_bool(
            self._safe,
            "router.group.add",
            {"id": str(group.gid), "spec": group_spec(group, _legacy_pickle(group))},
        )

    def perspective_group_enable(self, gid):
        return self._defer(self._safe, "router.group.enable", {"id": str(gid)})

    def perspective_group_disable(self, gid):
        return self._defer(self._safe, "router.group.disable", {"id": str(gid)})

    def perspective_group_remove(self, gid):
        return self._defer(self._safe, "router.group.remove", {"id": str(gid)})

    def perspective_group_remove_all(self):
        return self._defer(self._safe, "router.group.remove_all", {})

    def _group_get_all(self):
        rows = self._safe("router.group.list", {})
        if rows is False:
            return False
        result = []
        for row in rows:
            spec = row["spec"]
            result.append(group_object(spec, row.get("id")))
        return pickle.dumps(result, pickle.HIGHEST_PROTOCOL)

    def perspective_group_get_all(self):
        return self._defer(self._group_get_all)

    def perspective_user_add(self, value):
        user = pickle.loads(value)
        return self._defer_bool(
            self._safe,
            "router.user.add",
            {"id": user.username, "spec": user_spec(user, _legacy_pickle(user))},
        )

    def _username_for_uid(self, uid):
        rows = self._safe("router.user.list", {})
        if rows is False:
            return None
        for row in rows:
            if str(row["spec"].get("external_id")) == str(uid):
                return row["id"]
        return None

    def _user_action(self, method, uid):
        username = self._username_for_uid(uid)
        if username is None:
            return False
        return self._safe(method, {"id": username})

    def perspective_user_remove(self, uid):
        return self._defer(self._user_action, "router.user.remove", uid)

    def perspective_user_enable(self, uid):
        return self._defer(self._user_action, "router.user.enable", uid)

    def perspective_user_disable(self, uid):
        return self._defer(self._user_action, "router.user.disable", uid)

    def perspective_user_remove_all(self):
        return self._defer(self._safe, "router.user.remove_all", {})

    def perspective_user_authenticate(self, username, password):
        return self._defer(self._safe, "router.user.authenticate", {"username": username, "password": password})

    def _user_get_all(self, gid):
        rows = self._safe("router.user.list", {})
        if rows is False:
            return False
        result = []
        for row in rows:
            spec = row["spec"]
            if gid is not None and str(spec.get("group_id")) != str(gid):
                continue
            result.append(user_object(spec, row.get("id")))
        return pickle.dumps(result, pickle.HIGHEST_PROTOCOL)

    def perspective_user_get_all(self, gid=None):
        return self._defer(self._user_get_all, gid)

    def _quota(self, method, uid, cred, quota, value):
        username = self._username_for_uid(uid)
        if username is None:
            return False
        return self._safe(method, {"id": username, "cred": cred, "quota": quota, "value": value})

    def perspective_user_set_quota(self, uid, cred, quota, value):
        return self._defer(self._quota, "router.user.set_quota", uid, cred, quota, value)

    def perspective_user_update_quota(self, uid, cred, quota, value):
        return self._defer(self._quota, "router.user.update_quota", uid, cred, quota, value)

    def _put_ordered(self, method, value, order, direction):
        obj = pickle.loads(value)
        spec = route_spec(obj, order, direction, base64.b64encode(value).decode("ascii"))
        return self._safe(method, {"order": order, "spec": spec})

    def perspective_mtroute_add(self, route, order):
        return self._defer_bool(self._put_ordered, "router.mtroute.add", route, order, "mt")

    def perspective_moroute_add(self, route, order):
        return self._defer_bool(self._put_ordered, "router.moroute.add", route, order, "mo")

    def perspective_mtroute_remove(self, order):
        return self._defer(self._safe, "router.mtroute.remove", {"order": order})

    def perspective_moroute_remove(self, order):
        return self._defer(self._safe, "router.moroute.remove", {"order": order})

    def perspective_mtroute_flush(self):
        return self._defer(self._safe, "router.mtroute.flush", {})

    def perspective_moroute_flush(self):
        return self._defer(self._safe, "router.moroute.flush", {})

    def _ordered_get_all(self, method, direction, interceptors=False):
        rows = self._safe(method, {})
        if rows is False:
            return False
        values = []
        for row in rows:
            builder = interceptor_object if interceptors else route_object
            values.append({int(row["order"]): builder(row["spec"], direction)})
        return pickle.dumps(values, pickle.HIGHEST_PROTOCOL)

    def perspective_mtroute_get_all(self):
        return self._defer(self._ordered_get_all, "router.mtroute.list", "mt")

    def perspective_moroute_get_all(self):
        return self._defer(self._ordered_get_all, "router.moroute.list", "mo")

    def _put_interceptor(self, method, value, order):
        obj = pickle.loads(value)
        spec = interceptor_spec(obj, order, base64.b64encode(value).decode("ascii"))
        return self._safe(method, {"order": order, "spec": spec})

    def perspective_mtinterceptor_add(self, value, order):
        return self._defer_bool(self._put_interceptor, "router.mtinterceptor.add", value, order)

    def perspective_mointerceptor_add(self, value, order):
        return self._defer_bool(self._put_interceptor, "router.mointerceptor.add", value, order)

    def perspective_mtinterceptor_remove(self, order):
        return self._defer(self._safe, "router.mtinterceptor.remove", {"order": order})

    def perspective_mointerceptor_remove(self, order):
        return self._defer(self._safe, "router.mointerceptor.remove", {"order": order})

    def perspective_mtinterceptor_flush(self):
        return self._defer(self._safe, "router.mtinterceptor.flush", {})

    def perspective_mointerceptor_flush(self):
        return self._defer(self._safe, "router.mointerceptor.flush", {})

    def perspective_mtinterceptor_get_all(self):
        return self._defer(self._ordered_get_all, "router.mtinterceptor.list", "mt", True)

    def perspective_mointerceptor_get_all(self):
        return self._defer(self._ordered_get_all, "router.mointerceptor.list", "mo", True)


class ClientManagerAvatar(BaseAvatar):
    def perspective_persist(self, profile="jcli-prod"):
        return self._defer(self._safe, "profile.persist", {"profile": profile, "scope": "connectors"})

    def perspective_load(self, profile="jcli-prod"):
        return self._defer(self._safe, "profile.load", {"profile": profile, "scope": "connectors"})

    def perspective_is_persisted(self):
        return self._defer(self._safe, "profile.is_persisted", {})

    def perspective_connector_add(self, value):
        config = pickle.loads(value)
        return self._defer_bool(
            self._safe,
            "client.connector.add",
            {"config": connector_spec(config), "start": False},
        )

    def perspective_connector_remove(self, cid):
        return self._defer(self._safe, "client.connector.remove", {"id": cid})

    def _list(self):
        rows = self._safe("client.connector.list", {})
        if rows is False:
            return False
        return [self._details(row) for row in rows]

    def perspective_connector_list(self):
        return self._defer(self._list)

    def perspective_connector_start(self, cid):
        return self._defer_bool(self._safe, "client.connector.start", {"id": cid})

    def perspective_connector_stop(self, cid, delQueues=False):
        return self._defer_bool(
            self._safe,
            "client.connector.stop",
            {"id": cid, "delete_queues": delQueues},
        )

    def perspective_connector_stopall(self, delQueues=False):
        return self._defer(self._safe, "client.connector.stop_all", {"delete_queues": delQueues})

    def perspective_service_status(self, cid):
        result = self._defer(self._safe, "client.connector.status", {"id": cid})
        result.addCallback(lambda value: False if value is False else int(bool(value["desired_started"])))
        return result

    def perspective_session_state(self, cid):
        return self._defer(self._session_state, cid)

    def _session_state(self, cid):
        row = self._safe("client.connector.get", {"id": cid})
        return False if row is False else self._legacy_session_state(row)

    @staticmethod
    def _legacy_session_state(row):
        observed = row.get("observed")
        if observed != "BOUND":
            return observed if observed and observed.startswith("BOUND_") else "NONE"
        bind = row.get("config", {}).get("bind", "transceiver")
        return {
            "transceiver": "BOUND_TRX",
            "transmitter": "BOUND_TX",
            "receiver": "BOUND_RX",
        }.get(bind, "BOUND_TRX")

    @classmethod
    def _details(cls, row):
        return {
            "id": row["config"]["cid"],
            "session_state": cls._legacy_session_state(row),
            "service_status": int(bool(row["desired_started"])),
            "start_count": int(row.get("start_count", 0)),
            "stop_count": int(row.get("stop_count", 0)),
        }

    def _one_details(self, cid):
        row = self._safe("client.connector.get", {"id": cid})
        return False if row is False else self._details(row)

    def perspective_connector_details(self, cid):
        return self._defer(self._one_details, cid)

    def _config(self, cid):
        spec = self._safe("client.connector.config", {"id": cid})
        if spec is False:
            return False
        return pickle.dumps(connector_object(spec), pickle.HIGHEST_PROTOCOL)

    def perspective_connector_config(self, cid):
        return self._defer(self._config, cid)

    def _submit(self, uid, cid, pdu_value, submit_sm_bill, priority,
                validity_period, source_connector, dlr_url, dlr_level,
                dlr_method, dlr_connector):
        pdu = pickle.loads(pdu_value)
        return self._safe("router.submit_sm", {
            "user_id": str(uid),
            "connector_id": str(cid),
            "pdu_wires": pdu_wires(pdu),
            "bill": bill_spec(submit_sm_bill),
            "priority": priority,
            "validity_period": "" if validity_period is None else str(validity_period),
            "source_connector": source_connector,
            "dlr_url": dlr_url or "",
            "dlr_level": dlr_level,
            "dlr_method": dlr_method,
            "dlr_connector": dlr_connector or "",
        })

    def perspective_submit_sm(self, uid, cid, SubmitSmPDU, submit_sm_bill, priority=1, validity_period=None,
                              pickled=True, dlr_url=None, dlr_level=1, dlr_method="POST", dlr_connector=None,
                              source_connector="httpapi"):
        if not pickled:
            SubmitSmPDU = pickle.dumps(SubmitSmPDU, pickle.HIGHEST_PROTOCOL)
        return self._defer(
            self._submit,
            uid,
            cid,
            SubmitSmPDU,
            submit_sm_bill,
            priority,
            validity_period,
            source_connector,
            dlr_url,
            dlr_level,
            dlr_method,
            dlr_connector,
        )


class SMPPServerAvatar(BaseAvatar):
    def perspective_list_bound_systemids(self):
        return self._defer(self._safe, "smpps.list_bound_systemids", {})

    def _deliver(self, system_id, value, pickled):
        pdu = pickle.loads(value) if pickled else value
        return self._safe("smpps.deliverer_send_request", {
            "system_id": system_id,
            "pdu_wire": pdu_wire(pdu),
        })

    def perspective_deliverer_send_request(self, system_id, pdu, pickled=True):
        return self._defer(self._deliver, system_id, pdu, pickled)

    def perspective_unbind(self, system_id):
        return self._defer(self._safe, "smpps.unbind", {"id": system_id})

    def perspective_ban(self, system_id):
        return self._defer(self._safe, "smpps.ban", {"id": system_id})


class InterceptorAvatar(BaseAvatar):
    def _run(self, py_code, value):
        routable = pickle.loads(value)
        namespace = {
            "routable": routable,
            "smpp_status": None,
            "http_status": None,
            "extra": {},
            "hashlib": hashlib,
            "re": re,
            "json": json,
            "datetime": datetime,
            "math": math,
            "struct": struct,
        }
        try:
            eval(CompiledNode().get(py_code), namespace)
        except Exception as exc:
            self.log.error("legacy interceptor execution failed: %s", exc)
            return False
        smpp_status = namespace["smpp_status"]
        http_status = namespace["http_status"]
        if smpp_status is not None or http_status is not None:
            if not isinstance(smpp_status, int):
                smpp_status = 255
            if not isinstance(http_status, int):
                http_status = 520
            return {
                "http_status": http_status,
                "smpp_status": smpp_status,
                "extra": namespace["extra"],
            }
        return pickle.dumps(namespace["routable"], pickle.HIGHEST_PROTOCOL)

    def perspective_run_script(self, pyCode, routable):
        return self._defer(self._run, pyCode, routable)
