from twisted.internet import defer
from test_jcli import jCliTestCases
    
class SmppccmTestCase(jCliTestCases):
    # Wait delay for 
    wait = 0.3
    
    def add_connector(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'smppccm -a', 'expect': r'Adding a new connector\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added connector \[', 'wait': self.wait})

        return self._test(finalPrompt, commands)
    
    def test_list(self):
        commands = [{'command': 'smppccm -l', 'expect': r'Total: 0'}]
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
    def test_add_with_invalid_configkey(self):
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
                        'Total: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'cid operator_5'},
                         {'command': 'ko'}, ]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -l', 'expect': r'Total: 0'}]
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
                        'elink_interval 10', 
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
        commands = [{'command': 'smppccm -s operator_6', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)
        
    @defer.inlineCallbacks
    def test_show_invalid_cid(self):
        commands = [{'command': 'smppccm -s invalid_cid', 'expect': r'Unknown connector\: invalid_cid'}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_update_cid(self):
        extraCommands = [{'command': 'cid operator_7'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_7', 'expect': r'Updating connector id \[operator_7\]\: \(ok\: save, ko\: exit\)'},
                    {'command': 'cid 2222', 'expect': r'Connector id can not be modified !'}]
        yield self._test(r'> ', commands)
    
    @defer.inlineCallbacks
    def test_update(self):
        extraCommands = [{'command': 'cid operator_8'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u operator_8', 'expect': r'Updating connector id \[operator_8\]\: \(ok\: save, ko\: exit\)'},
                    {'command': 'port 2222'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[operator_8\]'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_update_and_show(self):
        cid = 'operator_9'
        extraCommands = [{'command': 'cid %s' % cid}]
        yield self.add_connector(r'jcli : ', extraCommands)

        commands = [{'command': 'smppccm -u %s' % cid, 'expect': r'Updating connector id \[%s\]\: \(ok\: save, ko\: exit\)' % cid},
                    {'command': 'port 222233'},
                    {'command': 'ok', 'expect': r'Successfully updated connector \[%s\]' % cid}]
        yield self._test(r'jcli : ', commands)
    
        expectedList = ['ripf 0', 
                        'con_fail_delay 10', 
                        'dlr_expiry 86400', 'coding 0', 
                        'submit_throughput 1', 
                        'elink_interval 10', 
                        'bind_to 30', 
                        'port 222233', 
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
        commands = [{'command': 'smppccm -s operator_9', 'expect': expectedList}]
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
    
        commands = [{'command': 'smppccm -r %s' % cid, 'expect': r'Successfully removed connector id\:operator_10'}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_remove_started_connector(self):
        # Add
        cid = 'operator_11'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 'expect': r'Successfully started connector id\:%s' % cid}]
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
                        'Total: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)

    @defer.inlineCallbacks
    def test_start(self):
        # Add
        cid = 'operator_13'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 'expect': r'Successfully started connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)
    
    @defer.inlineCallbacks
    def test_start_and_list(self):
        # Add
        cid = 'operator_14'
        extraCommands = [{'command': 'cid %s' % cid},
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 'expect': r'Successfully started connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)
    
        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_14                         started None             1      0    ', 
                        'Total: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
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
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 'expect': r'Failed stopping connector, check log for details'}]
        yield self._test(r'jcli : ', commands)
    
        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 'expect': r'Successfully started connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)
    
        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 'expect': r'Successfully stopped connector id\:%s' % cid}]
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
                         {'command': 'con_fail_retry 0'}]
        yield self.add_connector(r'jcli : ', extraCommands)

        # Start
        commands = [{'command': 'smppccm -1 %s' % cid, 'expect': r'Successfully started connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)
    
        # Stop
        commands = [{'command': 'smppccm -0 %s' % cid, 'expect': r'Successfully stopped connector id\:%s' % cid}]
        yield self._test(r'jcli : ', commands)
    
        # List
        expectedList = ['#Connector id                        Service Session          Starts Stops', 
                        '#operator_16                         stopped None             1      1    ', 
                        'Total: 1']
        commands = [{'command': 'smppccm -l', 'expect': expectedList}]
        yield self._test(r'jcli : ', commands)