from test_jcli import jCliWithoutAuthTestCases
    
class HttpccTestCases(jCliWithoutAuthTestCases):
    def add_httpcc(self, finalPrompt, extraCommands = []):
        sessionTerminated = False
        commands = []
        commands.append({'command': 'httpccm -a', 'expect': r'Adding a new Httpcc\: \(ok\: save, ko\: exit\)'})
        for extraCommand in extraCommands:
            commands.append(extraCommand)
            
            if extraCommand['command'] in ['ok', 'ko']:
                sessionTerminated = True
        
        if not sessionTerminated:
            commands.append({'command': 'ok', 'expect': r'Successfully added Httpcc \['})

        return self._test(finalPrompt, commands)
    
class BasicTestCases(HttpccTestCases):
    
    def test_add_with_minimum_args(self):
        extraCommands = [{'command': 'cid httpcc_1'}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        return self.add_httpcc(r'jcli : ', extraCommands)
    
    def test_add_without_minimum_args(self):
        extraCommands = [{'command': 'ok', 'expect': r'You must set these options before saving: url, method, cid'}]
        return self.add_httpcc(r'> ', extraCommands)
    
    def test_add_invalid_key(self):
        extraCommands = [{'command': 'cid httpcc_2'},
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'},
                         {'command': 'anykey anyvalue', 'expect': r'Unknown Httpcc key: anykey'}]
        return self.add_httpcc(r'jcli : ', extraCommands)
    
    def test_cancel_add(self):
        extraCommands = [{'command': 'cid httpcc_3'},
                         {'command': 'ko'}, ]
        return self.add_httpcc(r'jcli : ', extraCommands)

    def test_list(self):
        commands = [{'command': 'httpccm -l', 'expect': r'Total Httpccs: 0'}]
        return self._test(r'jcli : ', commands)
    
    def test_add_and_list(self):
        extraCommands = [{'command': 'cid httpcc_4'}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc('jcli : ', extraCommands)

        expectedList = ['#Httpcc id        Type                   Method URL', 
                        '#httpcc_4         HttpConnector          GET    http://127.0.0.1/bobo', 
                        'Total Httpccs: 1']
        commands = [{'command': 'httpccm -l', 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_add_cancel_and_list(self):
        extraCommands = [{'command': 'cid httpcc_5'},
                         {'command': 'ko'}, ]
        self.add_httpcc(r'jcli : ', extraCommands)

        commands = [{'command': 'httpccm -l', 'expect': r'Total Httpccs: 0'}]
        return self._test(r'jcli : ', commands)

    def test_show(self):
        cid = 'httpcc_6'
        extraCommands = [{'command': 'cid %s' % cid}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc('jcli : ', extraCommands)

        expectedList = ['HttpConnector\:',
                        'cid = %s' % cid,
                        'baseurl = http://127.0.0.1/bobo',
                        'method = GET']
        commands = [{'command': 'httpccm -s %s' % cid, 'expect': expectedList}]
        return self._test(r'jcli : ', commands)
    
    def test_update_not_available(self):
        cid = 'httpcc_7'
        extraCommands = [{'command': 'cid %s' % cid}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc(r'jcli : ', extraCommands)

        commands = [{'command': 'httpccm -u %s' % cid, 'expect': 'no such option\: -u'}]
        return self._test(r'jcli : ', commands)

    def test_remove_invalid_cid(self):
        commands = [{'command': 'httpccm -r invalid_cid', 'expect': r'Unknown Httpcc\: invalid_cid'}]
        return self._test(r'jcli : ', commands)
    
    def test_remove(self):
        cid = 'httpcc_8'
        extraCommands = [{'command': 'cid %s' % cid}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc(r'jcli : ', extraCommands)
    
        commands = [{'command': 'httpccm -r %s' % cid, 'expect': r'Successfully removed Httpcc id\:%s' % cid}]
        return self._test(r'jcli : ', commands)

    def test_remove_and_list(self):
        # Add
        cid = 'httpcc_9'
        extraCommands = [{'command': 'cid %s' % cid}, 
                         {'command': 'method POST'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc(r'jcli : ', extraCommands)
    
        # List
        expectedList = ['#Httpcc id        Type                   Method URL', 
                        '#httpcc_9         HttpConnector          POST   http://127.0.0.1/bobo', 
                        'Total Httpccs: 1']
        commands = [{'command': 'httpccm -l', 'expect': expectedList}]
        self._test(r'jcli : ', commands)
    
        # Remove
        commands = [{'command': 'httpccm -r %s' % cid, 'expect': r'Successfully removed Httpcc id\:%s' % cid}]
        self._test(r'jcli : ', commands)

        # List again
        commands = [{'command': 'httpccm -l', 'expect': r'Total Httpccs: 0'}]
        return self._test(r'jcli : ', commands)
    
class HttpccArgsTestCases(HttpccTestCases):
    
    def test_url(self):
        # URL validation test
        commands = [{'command': 'httpccm -a'},
                    {'command': 'cid httpcc_id'},
                    {'command': 'method get'},
                    {'command': 'url http://sdsd'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url ftp://127.0.0.1'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url ssl://127.0.0.1'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url httP://127.0.1'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url http://127.0.1'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url any thing'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url http://127.0.0/'},
                    {'command': 'ok', 'expect': r'Error\: HttpConnector url syntax is invalid'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        self._test(r'jcli : ', commands)
    
    def test_method(self):
        # POST method validation test
        commands = [{'command': 'httpccm -a'},
                    {'command': 'cid httpcc_id_post'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'method BET'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector method syntax'},
                    {'command': 'method SET'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector method syntax'},
                    {'command': 'method '},
                    {'command': 'ok', 'expect': r'Error: HttpConnector method syntax'},
                    {'command': 'method ALL'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector method syntax'},
                    {'command': 'method POST'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        self._test(r'jcli : ', commands)
    
        # GET method validation test
        commands = [{'command': 'httpccm -a'},
                    {'command': 'cid httpcc_id_get'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'method gEt'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        self._test(r'jcli : ', commands)

    def test_cid(self):
        commands = [{'command': 'httpccm -a'},
                    {'command': 'url http://127.0.0.1/Correct/Url'},
                    {'command': 'method post'},
                    {'command': 'cid a b'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector cid syntax'},
                    {'command': 'cid 1.2.3.4'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector cid syntax'},
                    {'command': 'cid !id!'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector cid syntax'},
                    {'command': 'cid this is an invalid id'},
                    {'command': 'ok', 'expect': r'Error: HttpConnector cid syntax'},
                    {'command': 'cid http-cc_valid_ID'},
                    {'command': 'ok', 'expect': r'Successfully added'},
                    ]
        self._test(r'jcli : ', commands)
    
class HttpccStrTestCases(HttpccTestCases):
    
    def test_str(self):
        extraCommands = [{'command': 'cid httpcc_id'}, 
                         {'command': 'method GET'},
                         {'command': 'url http://127.0.0.1/bobo'}]
        self.add_httpcc(r'jcli : ', extraCommands)
    
        expectedList = ['HttpConnector:', 
                        'cid = httpcc_id', 
                        'baseurl = http://127.0.0.1/bobo',
                        'method = GET']
        commands = [{'command': 'httpccm -s httpcc_id', 'expect': expectedList}]
        self._test(r'jcli : ', commands)