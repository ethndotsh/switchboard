package engine

import (
	"fmt"
	"strconv"
	"strings"
)

const wasmPageSize = 64 << 10

// ParseByteSize parses sizes like "32mb", "64kb", or plain bytes; kb/mb/gb
// and kib/mib/gib suffixes all mean powers of two.
func ParseByteSize(s string) (int64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	if trimmed == "" {
		return 0, fmt.Errorf("empty size")
	}
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		factor int64
	}{
		{"gib", 1 << 30}, {"gb", 1 << 30}, {"g", 1 << 30},
		{"mib", 1 << 20}, {"mb", 1 << 20}, {"m", 1 << 20},
		{"kib", 1 << 10}, {"kb", 1 << 10}, {"k", 1 << 10},
		{"b", 1},
	} {
		if strings.HasSuffix(trimmed, unit.suffix) {
			multiplier = unit.factor
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
			break
		}
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if value <= 0 {
		return 0, fmt.Errorf("size %q must be positive", s)
	}
	return value * multiplier, nil
}

func bytesToWasmPages(n int64) uint32 {
	if n <= 0 {
		return 0
	}
	pages := (n + wasmPageSize - 1) / wasmPageSize
	if pages > 65536 {
		pages = 65536
	}
	return uint32(pages)
}
