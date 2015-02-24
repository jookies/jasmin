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
from jasmin.vendor.enum import Enum
from jasmin.vendor.smpp.pdu.namedtuple import namedtuple
from jasmin.vendor.smpp.pdu import gsm_constants

InformationElementIdentifier = Enum(*gsm_constants.information_element_identifier_name_map.keys())

InformationElement = namedtuple('InformationElement', 'identifier, data')

IEConcatenatedSM = namedtuple('IEConcatenatedSM', 'referenceNum, maximumNum, sequenceNum')
