from twisted.web.resource import Resource

from jasmin.protocols.http.stats import HttpAPIStatsCollector
from jasmin.protocols.smpp.stats import SMPPClientStatsCollector, SMPPServerStatsCollector

PROM_METRICS_HTTPAPI = {
    'request_count':            {'type': b'counter', 'help': b'Http request count.'},
    'interceptor_count':        {'type': b'counter', 'help': b'Successful http request count.'},
    'auth_error_count':         {'type': b'counter', 'help': b'Authentication error count.'},
    'route_error_count':        {'type': b'counter', 'help': b'Routing error count.'},
    'interceptor_error_count':  {'type': b'counter', 'help': b'Interceptor error count.'},
    'throughput_error_count':   {'type': b'counter', 'help': b'Throughput exceeded error count.'},
    'charging_error_count':     {'type': b'counter', 'help': b'Charging error count.'},
    'server_error_count':       {'type': b'counter', 'help': b'Server error count.'},
    'success_count':            {'type': b'counter', 'help': b'Successful http request count.'},
}
PROM_METRICS_SMPPC = {
    'connected_count':          {'type': b'counter', 'help': b'Cumulated number of successful connections.'},
    'disconnected_count':       {'type': b'counter', 'help': b'Cumulated number of disconnections.'},
    'bound_count':              {'type': b'counter', 'help': b'Number of bound sessions.'},
    'submit_sm_request_count':  {'type': b'counter', 'help': b'SubmitSm pdu requests count.'},
    'submit_sm_count':          {'type': b'counter', 'help': b'Complete SubmitSm transactions count.'},
    'deliver_sm_count':         {'type': b'counter', 'help': b'DeliverSm pdu requests count.'},
    'data_sm_count':            {'type': b'counter', 'help': b'Complete DataSm transactions count.'},
    'interceptor_count':        {'type': b'counter', 'help': b'Interceptor calls count.'},
    'elink_count':              {'type': b'counter', 'help': b'EnquireLinks count.'},
    'throttling_error_count':   {'type': b'counter', 'help': b'Throttling errors count.'},
    'interceptor_error_count':  {'type': b'counter', 'help': b'Interception errors count.'},
    'other_submit_error_count': {'type': b'counter', 'help': b'Other errors count.'},
}
PROM_METRICS_SMPPS_API = {
    'connected_count':          {'type': b'counter', 'help': b'Number of connected sessions.'},
    'connect_count':            {'type': b'counter', 'help': b'Cumulated number of connect requests.'},
    'disconnect_count':         {'type': b'counter', 'help': b'Cumulated number of disconnect requests.'},
    'interceptor_count':        {'type': b'counter', 'help': b'Interceptor calls count.'},
    'bound_trx_count':          {'type': b'counter', 'help': b'Number of bound sessions in transceiver mode.'},
    'bound_rx_count':           {'type': b'counter', 'help': b'Number of bound sessions in receiver mode.'},
    'bound_tx_count':           {'type': b'counter', 'help': b'Number of bound sessions in transmitter mode.'},
    'bind_trx_count':           {'type': b'counter', 'help': b'Number of bind requests in transceiver mode.'},
    'bind_rx_count':            {'type': b'counter', 'help': b'Number of bind requests in receiver mode.'},
    'bind_tx_count':            {'type': b'counter', 'help': b'Number of bind requests in transmitter mode.'},
    'unbind_count':             {'type': b'counter', 'help': b'Cumulated number of unbind requests.'},
    'submit_sm_request_count':  {'type': b'counter', 'help': b'SubmitSm pdu requests count.'},
    'submit_sm_count':          {'type': b'counter', 'help': b'Complete SubmitSm transactions count.'},
    'deliver_sm_count':         {'type': b'counter', 'help': b'DeliverSm pdu requests count.'},
    'data_sm_count':            {'type': b'counter', 'help': b'Complete DataSm transactions count.'},
    'elink_count':              {'type': b'counter', 'help': b'EnquireLinks count.'},
    'throttling_error_count':   {'type': b'counter', 'help': b'Throttling errors count.'},
    'interceptor_error_count':  {'type': b'counter', 'help': b'Interception errors count.'},
    'other_submit_error_count': {'type': b'counter', 'help': b'Other errors count.'},
}


class Metrics(Resource):
    isleaf = True

    def __init__(self, SMPPClientManagerPB, log):
        Resource.__init__(self)

        self.SMPPClientManagerPB = SMPPClientManagerPB
        self.log = log

    def render_GET(self, request):
        """
        /metrics request processing, used for exporting prometheus metrics
        """

        self.log.debug("Rendering /metrics response with args: %s from %s",
                       request.args, request.getClientIP())

        request.responseHeaders.addRawHeader(b"content-type", b"text/plain")
        request.setResponseCode(200)

        # Init response payload
        response = []

        # Fill httpapi stats
        _s = HttpAPIStatsCollector().get()
        for metric, descriptor in PROM_METRICS_HTTPAPI.items():
            response.extend([
                b'# TYPE httpapi_%s %s' % (metric.encode(), descriptor['type']),
                b'# HELP httpapi_%s %s' % (metric.encode(), descriptor['help']),
                ('httpapi_%s %s' % (metric, _s.get(metric))).encode(),
            ])

        # Fill smppcs stats
        _connectors = self.SMPPClientManagerPB.perspective_connector_list()
        _stats = {}
        for metric, descriptor in PROM_METRICS_SMPPC.items():
            if len(_connectors) > 0:
                response.extend([
                    b'# TYPE smppc_%s %s' % (metric.encode(), descriptor['type']),
                    b'# HELP smppc_%s %s' % (metric.encode(), descriptor['help']),
                ])

            for _connector in _connectors:
                _cid = _connector['id']
                _s = _stats.get(_cid, SMPPClientStatsCollector().get(_cid))

                response.extend([
                    ('smppc_%s{cid="%s"} %s' % (metric, _cid, _s.get(metric))).encode(),
                ])

        # Fill smpps stats
        _s = SMPPServerStatsCollector().get('smpps_01').getStats()
        for metric, descriptor in PROM_METRICS_SMPPS_API.items():
            response.extend([
                b'# TYPE smppsapi_%s %s' % (metric.encode(), descriptor['type']),
                b'# HELP smppsapi_%s %s' % (metric.encode(), descriptor['help']),
                ('smppsapi_%s %s' % (metric, _s.get(metric))).encode(),
            ])

        # Add padding
        response.extend([b'', b''])

        return b'\n'.join(response)
