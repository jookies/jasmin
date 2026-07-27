package picklecompat

// Code-generated smpp.pdu param-enum wire<->ordinal tables (the pickled
// Enum value). Regenerate only if smpp.pdu changes.

var addrTONWireToOrdinal = map[uint8]int{
	0: 1,
	1: 2,
	2: 3,
	3: 4,
	4: 5,
	5: 6,
	6: 7,
}
var addrTONOrdinalToWire = map[int]uint8{
	1: 0,
	2: 1,
	3: 2,
	4: 3,
	5: 4,
	6: 5,
	7: 6,
}

var addrNPIWireToOrdinal = map[uint8]int{
	0:  1,
	1:  2,
	3:  3,
	4:  4,
	6:  5,
	8:  6,
	9:  7,
	10: 8,
	14: 9,
	18: 10,
}
var addrNPIOrdinalToWire = map[int]uint8{
	1:  0,
	2:  1,
	3:  3,
	4:  4,
	5:  6,
	6:  8,
	7:  9,
	8:  10,
	9:  14,
	10: 18,
}

var priorityFlagWireToOrdinal = map[uint8]int{
	0: 1,
	1: 2,
	2: 3,
	3: 4,
}
var priorityFlagOrdinalToWire = map[int]uint8{
	1: 0,
	2: 1,
	3: 2,
	4: 3,
}

var replaceIfPresentWireToOrdinal = map[uint8]int{
	0: 1,
	1: 2,
}
var replaceIfPresentOrdinalToWire = map[int]uint8{
	1: 0,
	2: 1,
}

// data_coding int -> DataCodingDefault enum ordinal (default-scheme codings).
var dataCodingDefaultOrdinal = map[uint8]int{
	0:  1,  // SMSC_DEFAULT_ALPHABET
	1:  2,  // IA5_ASCII
	2:  3,  // OCTET_UNSPECIFIED
	3:  4,  // LATIN_1
	4:  5,  // OCTET_UNSPECIFIED_COMMON
	5:  6,  // JIS
	6:  7,  // CYRILLIC
	7:  8,  // ISO_8859_8
	8:  9,  // UCS2
	9:  10, // PICTOGRAM
	10: 11, // ISO_2022_JP
	13: 12, // EXTENDED_KANJI_JIS
	14: 13, // KS_C_5601
}

const dataCodingDefaultSchemeOrdinal = 2 // DataCodingScheme.DEFAULT
const (
	esmClassModeStoreForward    = 4 // EsmClassMode.STORE_AND_FORWARD
	esmClassModeDefault         = 1 // EsmClassMode.DEFAULT
	esmClassTypeDefault         = 1 // EsmClassType.DEFAULT
	esmClassGsmUDHI             = 1 // EsmClassGsmFeatures.UDHI_INDICATOR_SET
	regDeliveryReceiptNone      = 1 // RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED
	regDeliveryReceiptRequested = 2 // RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED
)
