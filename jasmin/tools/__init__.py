# Copyright (c) Jookies LTD <jasmin@jookies.net>
# See LICENSE for details.

"""Jasmin SMS Gateway by Jookies LTD <jasmin@jookies.net>"""

from smpp.pdu.pdu_types import RegisteredDeliveryReceipt, AddrNpi, AddrTon

enum_mapping = dict([(str(value), value) for value in list(RegisteredDeliveryReceipt) + list(AddrNpi) + list(AddrTon)])
enum_name_mapping = dict([(value.name, value) for value in list(RegisteredDeliveryReceipt) + list(AddrNpi) + list(AddrTon)])

def to_enum(str_val):
    try:
        return enum_mapping[str_val]
    except NameError:
        try:
            return enum_name_mapping[str_val]
        except:
            return str_val
