package scepwire

import "encoding/asn1"

const (
	maxEnvelopeBytes = 1 << 20
	maxBERDepth      = 32
)

// envelopeDER gives the algorithm-policy decoder a definite-length view of CMS
// BER (RFC 5652 section 2). It never replaces the signed bytes: callers verify
// the original CMS signature first and decrypt the original envelope afterward.
// Constructed OCTET STRINGs remain constructed, preserving ciphertext chunks.
func envelopeDER(raw []byte) ([]byte, error) {
	if len(raw) > maxEnvelopeBytes {
		return nil, ErrWire
	}
	der, used, err := berValue(raw, 0)
	if err != nil || used != len(raw) {
		return nil, ErrWire
	}
	return der, nil
}

// berValue reads one BER value and normalizes its encoded structure for SCEP parsing.
func berValue(raw []byte, depth int) ([]byte, int, error) {
	if len(raw) < 2 || depth > maxBERDepth || raw[0] == 0 {
		return nil, 0, ErrWire
	}
	tagEnd := 1
	if raw[0]&0x1f == 0x1f {
		for {
			if tagEnd >= len(raw)-1 || tagEnd > 5 {
				return nil, 0, ErrWire
			}
			b := raw[tagEnd]
			tagEnd++
			if b&0x80 == 0 {
				break
			}
		}
	}
	position := tagEnd + 1
	lengthByte := raw[tagEnd]
	indefinite := lengthByte == 0x80
	constructed := raw[0]&0x20 != 0
	if indefinite && !constructed {
		return nil, 0, ErrWire
	}
	length := int(lengthByte)
	if lengthByte > 0x80 {
		count := int(lengthByte & 0x7f)
		if count > 4 || count > len(raw)-position {
			return nil, 0, ErrWire
		}
		length = 0
		for range count {
			length = length*256 + int(raw[position])
			position++
			if length > len(raw) {
				return nil, 0, ErrWire
			}
		}
	}
	end := len(raw)
	if !indefinite {
		if length > len(raw)-position {
			return nil, 0, ErrWire
		}
		end = position + length
	}
	content := raw[position:end]
	if constructed {
		content = nil
		for {
			if indefinite && end-position >= 2 && raw[position] == 0 && raw[position+1] == 0 {
				end = position + 2
				break
			}
			if position == end && !indefinite {
				break
			}
			child, used, err := berValue(raw[position:end], depth+1)
			if err != nil {
				return nil, 0, err
			}
			content = append(content, child...)
			position += used
		}
	}
	// RawValue preserves the original tag while encoding a definite length.
	// High tag numbers are left to encoding/asn1's bounded tag decoder.
	header := append([]byte(nil), raw[:tagEnd]...)
	header = append(header, 0)
	var value asn1.RawValue
	if _, err := asn1.Unmarshal(header, &value); err != nil {
		return nil, 0, ErrWire
	}
	value.FullBytes, value.Bytes = nil, content
	der, err := asn1.Marshal(value)
	if err != nil {
		return nil, 0, ErrWire
	}
	return der, end, nil
}
