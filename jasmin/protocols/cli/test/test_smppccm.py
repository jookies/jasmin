import pickle
from twisted.internet import defer, reactor
from test_jcli import jCliWithoutAuthTestCases
from jasmin.protocols.smpp.test.smsc_simulator import *

class SmppccmTestCases(jCliWithoutAuthTestCases):
    # Wait delay for 
    wait = 0.6

    def add_connector(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'smppccm -a', 'expect': r'Adding a new connector\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 
                             'expect': r'Successfully added connector \[', 
                             'wait': self.wait})

        return self._test(finalPrompt, commands)

class LastClientFactory(Factory):
    lastClient = None
    def buildProtocol(self, addr):
        self.lastClient = Factory.buildProtocol(self, addr)
        return self.lastClient

class HappySMSCTestCase(SmppccmTestCases):
    protocol = HappySMSC
    
    @defer.inlineCallbacks
    def setUp(self):
        yield SmppccmTestCases.setUp(self)
        
        self.smsc_f = LastClientFactory()
        self.smsc_f.protocol = self.protocol
        self.SMSCPort = reactor.listenTCP(0, self.smsc_f)
                
    @defer.inlineCallbacks
    def tearDown(self):
        SmppccmTestCases.tearDown(self)
        
        yield self.SMSCPort.stopListening()
    
class BasicTestCases(HappySMSCTestCase):
    
    def test_list(self):
        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        return self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'cid operator_1'}]
        yield self.add_connector(r'jcli : ', extraCommands)
    
    @defer.inlineCallbacks
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set at least connector id \(cid\) before saving !', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)
    
    @defer.inlineCallbacks
    def test_add_invalid_configkey(self):
        extraCommands = [{'command': 'cid operator_2'}, {'command': 'anykey anyvalue', 'expect': r'Unknown SMPPClientConfig key: anykey'}]
        yield self.add_connector(r'jcli : ', extraCommands)
    
    @defer.inlineCallbacks
    def test_add_invalid_configkey_value(self):
        extraCommands = [{'command': 'cid operator_3'}, 
                         {'command': 'port 22e'}, 
                         {'command': 'ok', 'expect': r'Error\: port must be an integer', 'wait': self.wait}]
        yield self.add_connector(r'> ', extraCommands)
    
    @defer.inlineCallbacks
    def test_cancel_add(self):
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'ko'}, ]
        yield self.add_connector(r'jcli : ', extraCommands)
    
    @defer.inlineCallbacks
    def test_add_and_list(self):
        extraCommands = [{'command': 'cid operator_4'}]
        yield self.add_connector('jcli : ', extraCommands)

        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_4                          stopped None             0      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'cid operator_5'},
                         {'command': 'ko'}, ]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_add_and_show(self):
        cid = 'operator_6'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector('jcli : ', extraCommands)

        expectedList = ['ripf 0', 
                        'con_fail_delay 10', 
                        'dlr_expiry 86400', 'coding 0', 
                        'submit_throughput 1', 
                        'elink_interval 30', 
                        'bind_to 30', 
                        'port 2775', 
                        'con_fail_retry 1', 
                        'password password', 
                        'src_addr None', 
                        'bind_npi 1', 
                        'addr_range None', 
                        'dst_ton 1', 
                        'res_to 60', 
                        'def_msg_id 0', 
                        'priority 0', 
                        'con_loss_retry 1', 
                        'username smppclient', 
                        'dst_npi 1', 
                        'validity None', 
                        'requeue_delay 120', 
                        'host 127.0.0.1', 
                        'src_npi 1', 
                        'trx_to 300', 
                        'logfile /var/log/jasmin/default-%s.log' % cid, 
                        'systype ', 
                        'cid %s' % cid, 
                        'loglevel 20', 
                        'bind transceiver', 
                        'proto_id None', 
                        'con_loss_delay 10', 
                        'bind_ton 0', 
                        'pdu_red_to 10', 
                        'src_ton 2']
        commands = [{'command': 'smppccm -s %s' % cid, 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
        
    @defer.inlineCallbacks
    def test_show_invalid_cid(self):
        commands = [{'command': 'smppccm -s invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_update_cid(self):
        cid = 'operator_7'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_7', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'cid 2222', 'expect': r'Connector id can not be modified !'}]
        yield self._test(r'> ', commands)
    
    @defer.inlineCallbacks
    def test_update(self):
        cid = 'operator_8'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_8', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'port 2222'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[%s\]' % cid}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_and_show(self):
        cid = 'operator_9'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u %s' % cid, 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'port 122223'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[%s\]' % cid}]
        yield self._test(r'jcli : ', commands)
    
        expectedList = ['ripf 0', 
                        'con_fail_delay 10', 
                        'dlr_expiry 86400', 'coding 0', 
                        'submit_throughput 1', 
                        'elink_interval 30', 
                        'bind_to 30', 
                        'port 122223', 
                        'con_fail_retry 1', 
                        'password password', 
                        'src_addr None', 
                        'bind_npi 1', 
                        'addr_range None', 
                        'dst_ton 1', 
                        'res_to 60', 
                        'def_msg_id 0', 
                        'priority 0', 
                        'con_loss_retry 1', 
                        'username smppclient', 
                        'dst_npi 1', 
                        'validity None', 
                        'requeue_delay 120', 
                        'host 127.0.0.1', 
                        'src_npi 1', 
                        'trx_to 300', 
                        'logfile /var/log/jasmin/default-%s.log' % cid, 
                        'systype ', 
                        'cid %s' % cid, 
                        'loglevel 20', 
                        'bind transceiver', 
                        'proto_id None', 
                        'con_loss_delay 10', 
                        'bind_ton 0', 
                        'pdu_red_to 10', 
                        'src_ton 2']
        commands = [{'command': 'smppccm -s %s' % cid, 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_invalid_cid(self):
        commands = [{'command': 'smppccm -r invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_remove(self):
        cid = 'operator_10'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)
    
        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_started_connector(self):
        # Add
        cid = 'operator_11'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_and_list(self):
        # Add
        cid = 'operator_12'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)
    
        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_12                         stopped None             0      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Remove
        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'smppccm -l', 'expect': r'Total connectors: 0'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start(self):
        # Add
        cid = 'operator_13'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0', 'wait': 0.6}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 
                    'expect': r'Successfully started connector id\:%s' % cid, 
                    'wait': 0.6}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_start_and_list(self):
        # Add
        cid = 'operator_14'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 1}]
        yield self._test(r'jcli : ', commands)
    
        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_14                         started BOUND_TRX        1      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 
                     'expect': r'Successfully stopped connector id',
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start_invalid_cid(self):
        commands = [{'command': 'smppccm -1 invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_stop(self):
        # Add
        cid = 'operator_15'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 'expect': r'Failed stopping connector, check log for details'}]
        yield self._test(r'jcli : ', commands)
    
        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)
    
        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 
                     'expect': r'Successfully stopped connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_stop_invalid_cid(self):
        commands = [{'command': 'smppccm -0 invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)
        
    @defer.inlineCallbacks
    def test_start_stop_and_list(self):
        # Add
        cid = 'operator_16'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'port %s' % self.SMSCPort.getHost().port}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 
                     'expect': r'Successfully started connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)
    
        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 
                     'expect': r'Successfully stopped connector id\:%s' % cid,
                     'wait': 0.6}]
        yield self._test(r'jcli : ', commands)
    
        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_16                         stopped NONE             1      1    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
    
class ParameterValuesTestCases(SmppccmTestCases):
    
    @defer.inlineCallbacks
    def test_log_level(self):
        # Set loglevel to WARNING
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'loglevel 30'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set loglevel to WARNING with non numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'loglevel WARNING'},
                         {'command': 'ok', 'expect': r'Error: SMPPClientConfig log_level syntax is invalid'}]
        yield self.add_connector(r'> ', extraCommands)

    @defer.inlineCallbacks
    def test_update_log_level(self):
        cid = 'operator_7'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_7', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'loglevel 30'},
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)

        commands = [{'command': 'smppccm -u operator_7', 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'loglevel WARNING'},
                    {'command': 'ok', 'expect': r'Error: SMPPClientConfig log_level syntax is invalid'}]
        yield self._test(r'> ', commands)

    @defer.inlineCallbacks
    def test_boolean_con_loss_retry(self):
        # Set to True
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'con_loss_retry 1'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to False
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'con_loss_retry No'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'con_loss_retry 31'},
                         {'command': 'ok', 'expect': r'Error: reconnectOnConnectionLoss must be a boolean'}]
        yield self.add_connector(r'>', extraCommands)

    @defer.inlineCallbacks
    def test_boolean_con_fail_retry(self):
        # Set to True
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'con_fail_retry 1'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to False
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'con_fail_retry No'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'con_fail_retry 31'},
                         {'command': 'ok', 'expect': r'Error: reconnectOnConnectionFailure must be a boolean'}]
        yield self.add_connector(r'>', extraCommands)

    @defer.inlineCallbacks
    def test_src_ton(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_ton_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'src_ton 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'src_ton 300'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'src_ton NATIONAL'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_dst_ton(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_ton_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'dst_ton 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'dst_ton 300'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'dst_ton NATIONAL'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_bind_ton(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_ton_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'bind_ton 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'bind_ton 300'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'bind_ton NATIONAL'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_src_npi(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_npi_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'src_npi 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'src_npi 5'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'src_npi WAP_CLIENT_ID'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_dst_npi(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_npi_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'dst_npi 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'dst_npi 5'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'dst_npi WAP_CLIENT_ID'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_bind_npi(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.addr_npi_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'bind_npi 3'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'bind_npi 5'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'bind_npi WAP_CLIENT_ID'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_priority(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.priority_flag_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'priority 2'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'priority 5'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'priority LEVEL_0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

    @defer.inlineCallbacks
    def test_ripf(self):
        # Set to a valid value (from jasmin.vendor.smpp.pdu.constants.replace_if_present_flap_name_map)
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'ripf 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid numeric value
        extraCommands = [{'command': 'cid operator_2'},
                         {'command': 'ripf 5'}]
        yield self.add_connector(r'jcli : ', extraCommands)
        
        # Set to invalid value
        extraCommands = [{'command': 'cid operator_3'},
                         {'command': 'ripf DO_NOT_REPLACE'}]
        yield self.add_connector(r'jcli : ', extraCommands)

class SMSCTestCases(HappySMSCTestCase):
    
    @defer.inlineCallbacks
    def setUp(self):
        yield HappySMSCTestCase.setUp(self)

        # A connector list to be stopped on tearDown
        self.startedConnectors = []

    @defer.inlineCallbacks
    def tearDown(self):
        # Stop all started connectors
        for startedConnector in self.startedConnectors:
            yield self.stop_connector(startedConnector)

        yield HappySMSCTestCase.tearDown(self)

    @defer.inlineCallbacks
    def start_connector(self, cid, finalPrompt = r'jcli : ', wait = 0.6, expect = None):
        commands = [{'command': 'smppccm -1 %s' % cid, 'wait': wait, 'expect': expect}]
        yield self._test(finalPrompt, commands)

        # Add cid to the connector list to be stopped in tearDown
        self.startedConnectors.append(cid)

    @defer.inlineCallbacks
    def stop_connector(self, cid, finalPrompt = r'jcli : ', wait = 0.6, expect = None):
        commands = [{'command': 'smppccm -0 %s' % cid, 'wait': wait, 'expect': expect}]
        yield self._test(finalPrompt, commands)

    @defer.inlineCallbacks
    def test_systype(self):
        """Testing for #64, will set systype key to any value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'systype 999999'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          started BOUND_TRX        1      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_systype(self):
        """Testing for #64, will set systype key to any value and start the connector to ensure
        it is correctly encoded in bind pdu"""

        # Add a connector, set systype and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Update the connector to set systype and start it
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'systype 999999'},
                    {'command': 'ok'}]
        yield self._test(r'jcli : ', commands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          started BOUND_TRX        1      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_quick_restart(self):
        "Testing for #68, restarting quickly a connector will loose its session state"

        # Add a connector and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1', wait = 3)

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          started BOUND_TRX        1      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Stop and start very quickly will lead to an error starting the connector because there were
        # no sufficient time for unbind to complete
        yield self.stop_connector('operator_1', finalPrompt = None, wait = 0)
        yield self.start_connector('operator_1', finalPrompt = None, 
                                    wait = 0, 
                                    expect= 'Failed starting connector, check log for details')

        # Wait
        exitDeferred = defer.Deferred()
        reactor.callLater(2, exitDeferred.callback, None)
        yield exitDeferred

        # List and assert it is stopped (start command errored)
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          stopped NONE             1      1    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_restart_on_update(self):
        "Testing for #68, updating a config key from RequireRestartKeys will lead to a quick restart"

        # Add a connector and start it
        extraCommands = [{'command': 'cid operator_1'},
                         {'command': 'port %s' % self.SMSCPort.getHost().port},]
        yield self.add_connector(r'jcli : ', extraCommands)
        yield self.start_connector('operator_1')

        # List and assert it is BOUND
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          started BOUND_TRX        1      0    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

        # Update loglevel which is in RequireRestartKeys and will lead to a connector restart
        commands = [{'command': 'smppccm -u operator_1'},
                    {'command': 'loglevel 10'},
                    {'command': 'ok', 'wait': 7, 
                        'expect': ['Restarting connector \[operator_1\] for updates to take effect ...',
                                   'Failed starting connector, will retry in 5 seconds',
                                   'Successfully updated connector \[operator_1\]']},]
        yield self._test(r'jcli : ', commands)

        # List and assert it is started (restart were successful)
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_1                          started BOUND_TRX        2      1    ', 
                        'Total connectors: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
