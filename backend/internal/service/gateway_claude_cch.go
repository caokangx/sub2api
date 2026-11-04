package service

import (
	"bytes"
	"fmt"

	"github.com/cespare/xxhash/v2"
)

const (
	claudeCCHSeed         uint64 = 0x4D659218E32A3268
	claudeCCHMask                = 0xF_FFFF
	claudeCCHSearchWindow        = 150
)

var (
	claudeBillingMarker  = []byte("x-anthropic-billing-header:")
	claudeCCHPlaceholder = []byte("cch=00000")
)

type claudeCCHRange struct {
	start int
	end   int
}

// attestClaudeCCH reproduces Claude Code 2.1.284's native Bun request-body
// attestation. The native layer hashes retained byte ranges of the final
// serialized JSON while cch=00000 is still present, masks xxHash64 to 20 bits,
// then overwrites the five zero bytes in place.
//
// Model values and native routing fields are excluded exactly as in the Bun
// implementation. This was validated against all 160 captured Agent SDK
// /v1/messages request bodies (160/160 exact matches).
func attestClaudeCCH(body []byte) ([]byte, error) {
	marker := bytes.Index(body, claudeBillingMarker)
	if marker < 0 {
		return body, nil
	}
	searchFrom := marker + len(claudeBillingMarker)
	searchTo := min(searchFrom+claudeCCHSearchWindow, len(body))
	relative := bytes.Index(body[searchFrom:searchTo], claudeCCHPlaceholder)
	if relative < 0 {
		return nil, fmt.Errorf("Claude billing header is missing cch=00000 placeholder")
	}
	index := searchFrom + relative
	digest := claudeCCHDigest(body)
	value := fmt.Sprintf("%05x", digest&claudeCCHMask)
	next := append([]byte(nil), body...)
	copy(next[index+4:index+9], value)
	return next, nil
}

func claudeCCHDigest(body []byte) uint64 {
	retained := make([]byte, 0, len(body))
	cursor := 0
	field, hasField := nextClaudeCCHField(body, cursor)
	for {
		model, hasModel := nextClaudeCCHModelValue(body, cursor)
		var excluded claudeCCHRange
		consumedField := false
		switch {
		case hasModel && hasField && field.start < model.start:
			excluded, consumedField = field, true
		case hasModel:
			excluded = model
		case hasField:
			excluded, consumedField = field, true
		default:
			retained = append(retained, body[cursor:]...)
			digest := xxhash.NewWithSeed(claudeCCHSeed)
			_, _ = digest.Write(retained)
			return digest.Sum64()
		}
		retained = append(retained, body[cursor:excluded.start]...)
		cursor = excluded.end
		// Native model-value skips do not rescan the tail for routing fields.
		if consumedField {
			field, hasField = nextClaudeCCHField(body, cursor)
		}
	}
}

func nextClaudeCCHModelValue(body []byte, cursor int) (claudeCCHRange, bool) {
	const marker = `"model":"`
	offset := bytes.Index(body[cursor:], []byte(marker))
	if offset < 0 {
		return claudeCCHRange{}, false
	}
	start := cursor + offset + len(marker)
	endOffset := bytes.IndexByte(body[start:], '"')
	if endOffset < 0 {
		return claudeCCHRange{}, false
	}
	return claudeCCHRange{start: start, end: start + endOffset}, true
}

func nextClaudeCCHField(body []byte, cursor int) (claudeCCHRange, bool) {
	var candidates []claudeCCHRange
	if field, ok := claudeCCHArrayField(body, cursor, `"fallbacks":[`); ok {
		candidates = append(candidates, field)
	}
	if field, ok := claudeCCHStringField(body, cursor, `"fallback_credit_token":"`); ok {
		candidates = append(candidates, field)
	}
	if field, ok := claudeCCHNumberField(body, cursor, `"max_tokens":`); ok {
		candidates = append(candidates, field)
	}
	if len(candidates) == 0 {
		return claudeCCHRange{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.start < best.start {
			best = candidate
		}
	}
	return best, true
}

func claudeCCHArrayField(body []byte, cursor int, marker string) (claudeCCHRange, bool) {
	offset := bytes.Index(body[cursor:], []byte(marker))
	if offset < 0 {
		return claudeCCHRange{}, false
	}
	start := cursor + offset
	end, ok := claudeCCHArrayEnd(body, start+len(marker))
	if !ok {
		return claudeCCHRange{}, false
	}
	return claudeCCHFieldSeparator(body, cursor, claudeCCHRange{start: start, end: end}), true
}

func claudeCCHStringField(body []byte, cursor int, marker string) (claudeCCHRange, bool) {
	offset := bytes.Index(body[cursor:], []byte(marker))
	if offset < 0 {
		return claudeCCHRange{}, false
	}
	start := cursor + offset
	value := start + len(marker)
	endOffset := bytes.IndexByte(body[value:], '"')
	if endOffset < 0 {
		return claudeCCHRange{}, false
	}
	return claudeCCHFieldSeparator(body, cursor, claudeCCHRange{start: start, end: value + endOffset + 1}), true
}

func claudeCCHNumberField(body []byte, cursor int, marker string) (claudeCCHRange, bool) {
	search := cursor
	for {
		offset := bytes.Index(body[search:], []byte(marker))
		if offset < 0 {
			return claudeCCHRange{}, false
		}
		start := search + offset
		value := start + len(marker)
		end := value
		for end < len(body) && body[end] >= '0' && body[end] <= '9' {
			end++
		}
		if end > value {
			return claudeCCHFieldSeparator(body, cursor, claudeCCHRange{start: start, end: end}), true
		}
		search = value
	}
}

func claudeCCHFieldSeparator(body []byte, cursor int, field claudeCCHRange) claudeCCHRange {
	if field.end < len(body) && body[field.end] == ',' {
		field.end++
	} else if field.start > cursor && body[field.start-1] == ',' {
		field.start--
	}
	return field
}

func claudeCCHArrayEnd(body []byte, cursor int) (int, bool) {
	depth := 1
	quoted := false
	escaped := false
	for cursor < len(body) {
		value := body[cursor]
		cursor++
		if quoted {
			switch {
			case escaped:
				escaped = false
			case value == '\\':
				escaped = true
			case value == '"':
				quoted = false
			}
			continue
		}
		switch value {
		case '"':
			quoted = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return cursor, true
			}
		}
	}
	return 0, false
}
