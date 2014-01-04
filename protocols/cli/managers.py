def FilterSessionArgs(fn):
    'Validate args before passing to session handler'
    def filter_cmd_and_call(self, *args, **kwargs):
        cmd = args[0]

        # Exit session if requested
        if cmd == 'ko':
            return self.stopSession()
        # Don't let use to quit
        if cmd == 'quit':
            return self.protocol.sendData('Exit session before quitting')
        
        return fn(self, *args, **kwargs)
    return filter_cmd_and_call

class Manager:
    # A prompt to display when inside an interactive session
    trxPrompt = '> '
    
    def startSession(self, sessionHandler, annoucement = None):
        'Switch prompt and hand user inputs directly to sessionHandler'

        self.protocol.lineCallback = sessionHandler
        self.backupPrompt = self.protocol.prompt
        self.protocol.prompt = self.trxPrompt
        self.sessBuffer = {}
        # Dont provide completitions inside a session
        self.protocol.keyHandlers['\t'] = self.handle_TAB
        if annoucement is not None:
            self.protocol.sendData(annoucement)

    def stopSession(self):
        'Reset prompt and disable sessionHandler'

        self.protocol.lineCallback = None
        self.protocol.prompt = self.backupPrompt
        self.sessBuffer = {}
        self.protocol.keyHandlers['\t'] = self.protocol.handle_TAB
        self.protocol.sendData()
    
    def handle_TAB(self):
        'Tab completition is disabled inside a session'
        self.lineBuffer = ''

    def __init__(self, protocol, pb):
        self.protocol = protocol
        self.pb = pb