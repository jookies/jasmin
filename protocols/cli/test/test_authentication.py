import random
from test_jcli import jCliWitAuthTestCases
from passlib.hash import sha256_crypt
    
class AuthenticationTestCases(jCliWitAuthTestCases):
    
    def test_password_prompt(self):
        commands = [{'command': 'AnyUsername'}]
        return self._test(r'Password: ', commands)
    
    def test_auth_failure(self):
        commands = [{'command': 'AnyUsername'},
                    {'command': 'AnyPassword%s' % random.randrange(100, 200), 'expect': 'Incorrect Username/Password.'}]
        return self._test(r'Username: ', commands)
    
    def test_auth_success(self):
        testPassword = 'AnyPassword%s' % random.randrange(100, 200)
        self.JCliConfigInstance.admin_password = sha256_crypt.encrypt(testPassword)
        
        commands = [{'command': self.JCliConfigInstance.admin_username},
                    {'command': testPassword, 'expect': 'Welcome to Jasmin console'}]
        return self._test(r'jcli : ', commands)