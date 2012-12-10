from jasmin.protocols.http.errors import ValidationError

class UrlArgsValidator:
    def __init__(self, request, fields):
        self.fields = fields
        self.request = request
        
    def validate(self):
        args = self.request.args
        
        if len(args) == 0:
            raise ValidationError('Mandatory arguments not found, please refer to the HTTPAPI specifications.')
        
        for arg in args:
            value = args[arg][0]
            # Check for unknown args
            if arg not in self.fields:
                raise ValidationError("Argument [%s] is unknown." % arg)
                            
            # Validate known args and check for mandatory fields
            for field in self.fields:
                fieldData = self.fields[field]
                    
                if field in args:
                    value = args[field][0]
                    # Validate known args
                    if 'pattern' in self.fields[field] and self.fields[field]['pattern'].match(value) is None:
                        raise ValidationError("Argument [%s] has an invalid value: [%s]." % (field, value))
                elif not fieldData['optional']:
                    raise ValidationError("Mandatory argument [%s] is not found." % field)

        return True