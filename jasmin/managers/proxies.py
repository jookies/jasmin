from jasmin.tools.proxies import ConnectedPB
from jasmin.tools.proxies import JasminPBProxy
from jasmin.vendor.smpp.pdu.operations import SubmitSM
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.Bills import SubmitSmBill

class SMPPClientManagerPBProxy(JasminPBProxy):
    'This is a proxy to SMPPClientManagerPB perspective broker'

    @ConnectedPB
    def version_release(self):
        return self.pb.callRemote('version_release')

    @ConnectedPB
    def version(self):
        return self.pb.callRemote('version')

    @ConnectedPB
    def persist(self, profile="jcli-prod"):
        return self.pb.callRemote('persist', profile)

    @ConnectedPB
    def load(self, profile="jcli-prod"):
        return self.pb.callRemote('load', profile)

    @ConnectedPB
    def is_persisted(self):
        return self.pb.callRemote('is_persisted')

    @ConnectedPB
    def add(self, config):
        if isinstance(config, SMPPClientConfig) is False:
            raise Exception("Object is not an instance of SMPPClientConfig")

        return self.pb.callRemote('connector_add', self.pickle(config))

    @ConnectedPB
    def remove(self, cid):
        return self.pb.callRemote('connector_remove', cid)

    @ConnectedPB
    def connector_list(self):
        return self.pb.callRemote('connector_list')

    @ConnectedPB
    def start(self, cid):
        return self.pb.callRemote('connector_start', cid)

    @ConnectedPB
    def stop(self, cid, delQueues=False):
        return self.pb.callRemote('connector_stop', cid, delQueues)

    @ConnectedPB
    def stopall(self, delQueues=False):
        return self.pb.callRemote('connector_stopall', delQueues)

    @ConnectedPB
    def session_state(self, cid):
        return self.pb.callRemote('session_state', cid)

    @ConnectedPB
    def service_status(self, cid):
        return self.pb.callRemote('service_status', cid)

    @ConnectedPB
    def connector_details(self, cid):
        return self.pb.callRemote('connector_details', cid)

    @ConnectedPB
    def connector_config(self, cid):
        """Once the returned deferred is fired, a pickled SMPPClientConfig
        is obtained as a result (if success)"""
        return self.pb.callRemote('connector_config', cid)

    @ConnectedPB
    def submit_sm(self, cid, SubmitSmPDU, submit_sm_bill):
        if not isinstance(SubmitSmPDU, SubmitSM):
            raise Exception("SubmitSmPDU is not an instance of SubmitSm")
        if not isinstance(submit_sm_bill, SubmitSmBill):
            raise Exception("submit_sm_bill is not an instance of SubmitSmBill")

        # Remove schedule_delivery_time / not supported right now
        if SubmitSmPDU.params['schedule_delivery_time'] is not None:
            SubmitSmPDU.params['schedule_delivery_time'] = None

        # Set the message priority
        if SubmitSmPDU.params['priority_flag'] is not None:
            priority_flag = SubmitSmPDU.params['priority_flag'].index
        else:
            priority_flag = 0

        # Set the message validity date
        if SubmitSmPDU.params['validity_period'] is not None:
            validity_period = SubmitSmPDU.params['validity_period'].strftime('%Y-%m-%d %H:%M:%S')
        else:
            # Validity period is not set, the SMS-C will set its own default
            # validity_period to this message
            validity_period = None

        return self.pb.callRemote(
            'submit_sm',
            cid=cid,
            SubmitSmPDU=self.pickle(SubmitSmPDU),
            submit_sm_bill=self.pickle(submit_sm_bill),
            priority=priority_flag,
            validity_period=validity_period)
