package common

import (
	"math"

	"github.com/tidwall/gjson"
)

// ParseGeminiUsageCount accepts exact non-negative ProtoJSON integers without
// converting through float64. The bound prevents excessive work on malformed
// numeric strings; exponent magnitudes are capped before they can overflow.
func ParseGeminiUsageCount(value gjson.Result) (int64, bool) {
	var raw string
	switch value.Type {
	case gjson.Number:
		raw = value.Raw
	case gjson.String:
		raw = value.Str
	default:
		return 0, false
	}
	if len(raw) == 0 || len(raw) > 4096 {
		return 0, false
	}
	i := 0
	negative := raw[i] == '-'
	if negative {
		i++
	}
	if i == len(raw) {
		return 0, false
	}
	digits := make([]byte, 0, len(raw))
	start := i
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		digits = append(digits, raw[i])
		i++
	}
	if i == start {
		return 0, false
	}
	fractionDigits := 0
	if i < len(raw) && raw[i] == '.' {
		i++
		start = i
		for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
			digits = append(digits, raw[i])
			i++
		}
		fractionDigits = i - start
		if fractionDigits == 0 {
			return 0, false
		}
	}
	exponent := 0
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		exponentNegative := false
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			exponentNegative = raw[i] == '-'
			i++
		}
		start = i
		for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
			if exponent < 100000 {
				exponent = exponent*10 + int(raw[i]-'0')
				if exponent > 100000 {
					exponent = 100000
				}
			}
			i++
		}
		if i == start {
			return 0, false
		}
		if exponentNegative {
			exponent = -exponent
		}
	}
	if i != len(raw) {
		return 0, false
	}
	first := 0
	for first < len(digits) && digits[first] == '0' {
		first++
	}
	if first == len(digits) {
		return 0, true
	}
	if negative {
		return 0, false
	}
	digits = digits[first:]
	scale := exponent - fractionDigits
	if scale < 0 {
		remove := -scale
		if remove > len(digits) {
			return 0, false
		}
		for _, digit := range digits[len(digits)-remove:] {
			if digit != '0' {
				return 0, false
			}
		}
		digits = digits[:len(digits)-remove]
		scale = 0
	}
	if len(digits)+scale > 19 {
		return 0, false
	}
	var count int64
	for _, digit := range digits {
		v := int64(digit - '0')
		if count > (math.MaxInt64-v)/10 {
			return 0, false
		}
		count = count*10 + v
	}
	for ; scale > 0; scale-- {
		if count > math.MaxInt64/10 {
			return 0, false
		}
		count *= 10
	}
	return count, true
}
