from jasmin.vendor.smpp.pdu.pdu_types import AddrTon, AddrNpi

routable.pdu.params['source_addr_ton'] = AddrTon.ALPHANUMERIC;;
routable.lockPduParam('source_addr_ton');
routable.pdu.params['source_addr_npi'] = AddrNpi.ISDN;
routable.lockPduParam('source_addr_npi');

routable.pdu.params['dest_addr_ton'] = AddrTon.INTERNATIONAL;
routable.lockPduParam('dest_addr_ton');
routable.pdu.params['dest_addr_npi'] = AddrNpi.ISDN;
routable.lockPduParam('dest_addr_npi');
