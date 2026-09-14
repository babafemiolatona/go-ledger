package money

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("amount is required")
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("amount must be > 0")
	}

	parts := strings.SplitN(s, ".", 2)
	wholeStr := parts[0]
	if wholeStr == "" {
		return 0, fmt.Errorf("invalid amount: %q", s)
	}
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %q", s)
	}

	var cents int64
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) == 0 || len(frac) > 2 {
			return 0, fmt.Errorf("invalid amount: %q, at most 2 decimal places", s)
		}
		if len(frac) == 1 {
			frac += "0"
		}
		c, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount: %q", s)
		}
		cents = c
	}
	amount := whole*100 + cents
	if amount <= 0 {
		return 0, fmt.Errorf("amount must be > 0")
	}
	return amount, nil
}

func FormatAmount(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}
