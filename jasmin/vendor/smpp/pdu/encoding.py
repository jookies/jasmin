"""
Copyright 2009-2010 Mozes, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either expressed or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
"""
from jasmin.vendor.smpp.pdu import pdu_types
from jasmin.vendor.smpp.pdu.error import PDUCorruptError

class IEncoder(object):

    def encode(self, value):
        """Takes an object representing the type and returns a byte string"""
        raise NotImplementedError()

    def decode(self, file):
        """Takes file stream in and returns an object representing the type"""
        raise NotImplementedError()
        
    def read(self, file, size):
        bytesRead = file.read(size)
        length = len(bytesRead)
        if length == 0:
            raise PDUCorruptError("Unexpected EOF", pdu_types.CommandStatus.ESME_RINVMSGLEN)
        if length != size:
            raise PDUCorruptError("Length mismatch. Expecting %d bytes. Read %d" % (size, length), pdu_types.CommandStatus.ESME_RINVMSGLEN)
        return bytesRead