package donationquota

import "context"

// Available is only a read-only hint. Reserve and Reconcile repeat the same
// check under the authoritative dispatch transaction.
func Available(ctx context.Context, q Reader, keyID, now int64, amounts Amounts) (bool, error) {
	views, err := Views(ctx, q, keyID, now)
	if err != nil {
		return false, err
	}
	for _, v := range views {
		if (v.Metric == "input_tokens" || v.Metric == "output_tokens") && !amounts.TokenBreakdown {
			return false, nil
		}
		remaining, err := parseMagnitude(v.Metric, v.Remaining)
		if err != nil {
			return false, ErrInvariant
		}
		if v.State == "limited" || amountFor(v.Metric, amounts).Big().Cmp(remaining.Big()) > 0 {
			return false, nil
		}
	}
	return true, nil
}
