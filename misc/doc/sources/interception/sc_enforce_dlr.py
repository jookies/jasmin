"This script will enforce sending message while asking for DLR"

from jasmin.vendor.smpp.pdu.pdu_types import RegisteredDeliveryReceipt, RegisteredDelivery

routable.pdu.params['registered_delivery'] = RegisteredDelivery(
    RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED)
